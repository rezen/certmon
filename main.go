package main

import (
	"github.com/boltdb/bolt"
	"github.com/go-redis/redis"
	"github.com/gorilla/websocket"
	tld "github.com/jpillora/go-tld"
	"github.com/json-iterator/go"
	elog "github.com/labstack/gommon/log"
	logging "github.com/op/go-logging"
	"github.com/pkg/errors"
	"io/ioutil"
	"os"
	"strings"
	"time"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"net/http"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type LogNotifier struct {
	Path string
}

func (l *LogNotifier) Notify(m Match) {
	file, err := os.OpenFile(l.Path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Error(err)
	}
	defer file.Close()
	_, err = file.WriteString(m.EntryString + "\n")
	if err != nil {
		log.Error(err)
	}
}

type RedisNotifier struct {
	Client  *redis.Client
	Channel string
}

func (r *RedisNotifier) Notify(m Match) {
	r.Client.Publish(r.Channel, m.EntryString)
}

type Notifier interface {
	Notify(m Match)
}

func CreateRedisClient() *redis.Client {
	redisHost := os.Getenv("REDIS_HOST")
	redisPort := os.Getenv("REDIS_PORT")

	if len(redisHost) == 0 {
		redisHost = "localhost"
	}

	if len(redisPort) == 0 {
		redisPort = "6379"
	}

	return redis.NewClient(&redis.Options{
		Addr:     redisHost + ":" + redisPort,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
}

type Match struct {
	Entry       Entry
	EntryString string
}
type Entry struct {
	Data        EntryData `json:"data"`
	MessageType string    `json:"message_type"`
	Domain      string    `json:"domain"`
}

type EntryData struct {
	CertIndex int      `json:"cert_index"`
	CertLink  string   `json:"cert_link"`
	LeafCert  LeafCert `json:"leaf_cert"`
	Seen      float32  `json:"seen"`
}

type LeafCert struct {
	AllDomains []string               `json:"all_domains"`
	Subject    Subject                `json:"subject"`
	Extensions map[string]interface{} `json:"extensions"`
	NotBefore  int                    `json:"not_before"`
	NotAfter   int                    `json:"not_after"`
}

type Counter struct {
	Values map[string]int `json:"values"`
}

func (c *Counter) Increment(key string) {
	c.Values[key] += 1
}

func CreateCounter() *Counter {
	return &Counter{map[string]int{
		"consumed":      0,
		"stream_errors": 0,
		"redis_errors":  0,
		"matched":       0,
		"tld_errors":    0,
	}}
}

type Subject struct {
	C          string `json:"c"`
	CN         string `json:"cn"`
	Aggregated string `json:"aggregated"`
}

var log = logging.MustGetLogger("example")

func CertStreamEventStream() (chan Entry, chan error) {
	entries := make(chan Entry)
	errs := make(chan error)

	go func() {
		for {
			c, _, err := websocket.DefaultDialer.Dial("wss://certstream.calidog.io", nil)

			if err != nil {
				errs <- errors.Wrap(err, "Error connecting to certstream! Sleeping a few seconds and reconnecting... ")
				time.Sleep(5 * time.Second)
				continue
			}

			defer c.Close()
			defer close(entries)

			for {
				var entry Entry
				err = c.ReadJSON(&entry)
				if err != nil {
					errs <- errors.Wrap(err, "Error decoding json frame!")
					c.Close()
					break
				}

				if entry.MessageType == "heartbeat" {
					continue
				}

				entries <- entry
			}
		}
	}()

	return entries, errs
}

type RedisStorage struct {
	Client  *redis.Client
	Counter *Counter
}

func (r *RedisStorage) Monitor(domain string) error {
	r.Client.HSet("watch_domains", domain, "1")
	return nil
}

func (r *RedisStorage) Record(match Match) error {
	r.Client.Publish("domains_found", match.EntryString)
	return nil
}

func (r *RedisStorage) IsMonitored(entry *Entry) (bool, error) {
	u, err := tld.Parse("https://" + entry.Data.LeafCert.Subject.CN)
	if err != nil {
		r.Counter.Increment("tld_errors")
		return false, err
	}
	entry.Domain = u.Domain + "." + u.TLD

	yes, err := r.Client.HExists("watch_domains", entry.Domain).Result()
	if err != nil {
		r.Counter.Increment("redis_errors")
	}
	return yes, err
}

type BoltStorage struct {
	DB         *bolt.DB
	BucketName string
	Counter    *Counter
	Logger     echo.Logger
}

type Store interface {
	Remove(domain string) error
	Monitor(domain string) error
	Matches(domain string) []Entry
	Record(match Match) error
	IsMonitored(entry *Entry) (bool, error)
	Domains() []string
}

func (s *BoltStorage) Domains() []string {
	domains := []string{}
	tx, err := s.DB.Begin(false)
	if err != nil {
		return domains
	}
	defer tx.Rollback()
	bucket := tx.Bucket([]byte(s.BucketName))

	bucket.ForEach(func(k, v []byte) error {
		domains = append(domains, string(k))
		return nil
	})
	return domains
}

func (s *BoltStorage) Monitor(domain string) error {
	return s.DB.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(s.BucketName))
		if err != nil {
			return err
		}
		bucket.Put([]byte(domain), []byte("1"))
		return nil
	})
}

func (s *BoltStorage) Remove(domain string) error {
	return s.DB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(s.BucketName))
		if bucket == nil {
			return nil
		}
		bucket.Delete([]byte(domain))

		tx.DeleteBucket([]byte("match_" + domain))
		return nil
	})
}

func (s *BoltStorage) Matches(domain string) []Entry {
	matches := []Entry{}
	s.DB.View(func(tx *bolt.Tx) error {
		// Assume bucket exists and has keys
		bucket := tx.Bucket([]byte("matches"))
		bucket = bucket.Bucket([]byte("match_" + domain))
		if bucket == nil {
			s.Logger.Error("No matches for ", domain)
			return nil
		}
		bucket.ForEach(func(k, v []byte) error {
			var entry Entry
			if err := json.Unmarshal(v, &entry); err != nil {
				panic(err)
			} else {
				matches = append(matches, entry)
			}
			return nil
		})
		return nil
	})

	return matches
}

func (s *BoltStorage) Record(match Match) error {
	return s.DB.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("matches"))
		if err != nil {
			return err
		}

		bucket, err = bucket.CreateBucketIfNotExists([]byte("match_" + match.Entry.Domain))
		if err != nil {
			return err
		}
		bucket.Put([]byte(time.Now().String()), []byte(match.EntryString))
		return nil
	})
	return nil
}

func (s *BoltStorage) IsMonitored(entry *Entry) (bool, error) {
	u, err := tld.Parse("https://" + entry.Data.LeafCert.Subject.CN)
	if err != nil {
		s.Counter.Increment("tld_errors")
		return false, err
	}
	entry.Domain = u.Domain + "." + u.TLD
	tx, err := s.DB.Begin(false)
	if err != nil {
		s.Counter.Increment("bolt_errors")
		return false, err
	}
	defer tx.Rollback()
	bucket := tx.Bucket([]byte(s.BucketName))
	v := bucket.Get([]byte(entry.Domain))
	return string(v) == "1", err
}

func Worker(storage Store, count *Counter, l echo.Logger) {
	stream, errs := CertStreamEventStream()
	ticker := time.NewTicker(60 * time.Second)
	quit := make(chan struct{})

	for {
		select {
		case <-quit:
			ticker.Stop()
			return
		case entry := <-stream:
			count.Increment("consumed")

			yes, err := storage.IsMonitored(&entry)
			if err != nil {
				continue
			}
			if yes {
				match := &Match{entry, ""}
				count.Increment("matched")

				l.Debug("Found match ", entry.Data.LeafCert.AllDomains)
				b, err := json.Marshal(entry)
				match.EntryString = string(b)

				storage.Record(*match)
				if err != nil {
					count.Increment("json_error")
				}
				/*
					for _, n := range notifiers {
						n.Notify(*match)
					}
				*/
			}

		case <-errs:
			count.Increment("stream_errors")
			// log.Error(err)
		case <-ticker.C:
			l.Info(count)

		}
	}
}

func main() {
	// @todo what if redis goes away?
	e := echo.New()
	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	db, err := bolt.Open("monitor.db", 0777, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		e.Logger.Error(err)
		panic(err)
	}
	defer db.Close()

	count := CreateCounter()
	// client := CreateRedisClient()
	// storage := &RedisStorage{client,count}
	storage := &BoltStorage{db, "monitoring", count, e.Logger}

	//  seedDomains(storage)
	e.Logger.SetLevel(elog.DEBUG)
	e.Logger.Info("--- Starting ---")
	/*
		notifiers := []Notifier{
			&LogNotifier{"matches.json"},
			&RedisNotifier{client, "domains_found"},
		}*/

	go Worker(storage, count, e.Logger)

	// Routes
	e.GET("/", func(c echo.Context) error {
		matches := storage.Matches("microsoft.com")
		return c.JSON(http.StatusOK, matches)
	})

	e.GET("/domain", func(c echo.Context) error {
		domains := storage.Domains()
		return c.JSON(http.StatusOK, domains)
	})

	e.GET("/matches", func(c echo.Context) error {
		matches := []Entry{}
		db.View(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("matches"))
			bucket.ForEach(func(k, v []byte) error {
				// @todo check if k contains match_
				b := bucket.Bucket(k)
				b.ForEach(func(k, v []byte) error {
					var entry Entry
					if err := json.Unmarshal(v, &entry); err != nil {
						e.Logger.Error(err)
					} else {
						matches = append(matches, entry)
					}
					return nil
				})
				return nil
			})
			return nil
		})
		return c.JSON(http.StatusOK, matches)
	})

	e.POST("/domain", func(c echo.Context) error {
		var data map[string]interface{}

		if err := c.Bind(&data); err != nil {
			return err
		}
		domain := data["domain"].(string)
		_, err := tld.Parse(domain)
		if err != nil {
			return c.String(http.StatusCreated, "Not a real tld")
		}

		storage.Monitor(domain)
		return c.JSON(http.StatusCreated, data)
	})

	e.GET("/domain/:name", func(c echo.Context) error {
		domain := c.Param("name")
		matches := storage.Matches(domain)
		return c.JSON(http.StatusCreated, matches)
	})

	e.DELETE("/domain/:name", func(c echo.Context) error {
		domain := c.Param("name")
		storage.Remove(domain)
		return c.String(http.StatusOK, "OK")
	})

	e.GET("/stats", func(c echo.Context) error {
		return c.JSON(http.StatusOK, count)
	})

	// Start server
	e.Logger.Fatal(e.Start(":1323"))
}

func seedDomains(s Store) {
	data, err := ioutil.ReadFile("domains.csv")
	if err != nil {
		return
	}

	domains := strings.Split(string(data), "\n")

	log.Info("Seeding with domains", len(domains))
	for _, v := range domains {
		s.Monitor(v)
	}
}
