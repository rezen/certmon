package certmon

import (
	"github.com/boltdb/bolt"
	tld "github.com/jpillora/go-tld"
	"github.com/json-iterator/go"
	elog "github.com/labstack/gommon/log"
	"io/ioutil"
	"strings"
	"time"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"net/http"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary






func Serve() {
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
	e.Logger.Fatal(e.Start(":9000"))
}

func seedDomains(s Store) {
	data, err := ioutil.ReadFile("domains.csv")
	if err != nil {
		return
	}

	domains := strings.Split(string(data), "\n")

	for _, v := range domains {
		s.Monitor(v)
	}
}
