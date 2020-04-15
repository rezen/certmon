package main

import (
	"io/ioutil"
	"strings"
	"github.com/go-redis/redis"
	"github.com/gorilla/websocket"
	tld "github.com/jpillora/go-tld"
	"github.com/json-iterator/go"
	logging "github.com/op/go-logging"
	"github.com/pkg/errors"
	"os"
	"time"
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
	Domain      string
	Entry       Entry
	EntryString string
}
type Entry struct {
	Data        EntryData `json:"data"`
	MessageType string    `json:"message_type"`
}

type EntryData struct {
	CertLink string   `json:"cert_link"`
	LeafCert LeafCert `json:"leaf_cert"`
	Seen     float32  `json:"seen"`
}

type LeafCert struct {
	AllDomains []string               `json:"all_domains"`
	Subject    Subject                `json:"subject"`
	Extensions map[string]interface{} `json:"extensions"`
	NotBefore  int                    `json:"not_before"`
	NotAfter   int                    `json:"not_after"`
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

func main() {
	// @todo what if redis goes away?

	client := CreateRedisClient()
	seedDomains(client)

	log.Info("--- Starting ---")
	counters := map[string]int{
		"consumed":      0,
		"stream_errors": 0,
		"redis_errors":  0,
		"matched":       0,
		"tld_errors":    0,
	}

	stream, errs := CertStreamEventStream()
	ticker := time.NewTicker(60 * time.Second)
	quit := make(chan struct{})

	notifiers := []Notifier{
		&LogNotifier{"matches.json"},
		&RedisNotifier{client, "domains_found"},
	}

	for {
		select {
		case entry := <-stream:
			counters["consumed"] += 1
			u, err := tld.Parse("https://" + entry.Data.LeafCert.Subject.CN)

			if err != nil {
				counters["tld_errors"] += 1
				log.Error()
				continue
			}
			domain := u.Domain + "." + u.TLD

			yes, err := client.HExists("watch_domains", domain).Result()
			if err != nil {
				counters["redis_errors"] += 1
				panic(err)
			}

			if yes {
				match := &Match{domain, entry, ""}
				counters["matched"] += 1

				log.Info("Found match ", entry.Data.LeafCert.AllDomains)
				b, err := json.Marshal(entry)
				match.EntryString = string(b)

				if err != nil {
					panic(err)
				}
				for _, n := range notifiers {
					n.Notify(*match)
				}
			} else {
			}

		case <-errs:
			counters["stream_errors"] += 1
			// log.Error(err)
		case <-ticker.C:
			log.Info("Handled", counters)
		case <-quit:
			ticker.Stop()
			return

		}
	}
}

func seedDomains(client *redis.Client) {
	data, err := ioutil.ReadFile("domains.csv")
	if err != nil {
		return
	}

	domains := strings.Split(string(data), "\n")
	
	log.Info("Seeding with domains", len(domains))
	for _, v := range domains {
		client.HSet("watch_domains", v, "1")
	}
}
