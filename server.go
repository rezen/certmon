package certmon

import (
	"github.com/boltdb/bolt"
	tld "github.com/jpillora/go-tld"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	elog "github.com/labstack/gommon/log"
	"net/http"
	"os"
	"time"
)

func Serve() {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	config := &Config{
		Stream:     "wss://certstream.calidog.io",
		Storage:    "bolt",
		LogLevel:   os.Getenv("LOGLEVEL"),
		ServerPort: ":9000",
		BoltPath:   "monitor.db",
	}

	var storage Store
	var db *bolt.DB
	var err error
	count := CreateCounter()

	if config.Storage == "bolt" {
		db, err = bolt.Open(config.BoltPath, 0777, &bolt.Options{Timeout: 1 * time.Second})
		if err != nil {
			e.Logger.Error(err)
			panic(err)
		}
		storage = &BoltStorage{db, "monitoring", count, e.Logger}
		defer db.Close()
	} else {
		client := CreateRedisClient()
		storage = &RedisStorage{client, count}
	}

	if config.LogLevel == "debug" {
		e.Logger.SetLevel(elog.DEBUG)
	}

	e.Logger.Info("--- Starting ---")
	/*
		notifiers := []Notifier{
			&LogNotifier{"matches.json"},
			&RedisNotifier{client, "domains_found"},
		}
	*/

	go Worker(*config, storage, count, e.Logger)

	// Routes
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "1.0")
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
	/*
	e.GET("/extensions", func(c echo.Context) error {
		keys := map[string]bool{}
		db.View(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("matches"))
			if bucket == nil {
				return nil
			}
			bucket.ForEach(func(k, v []byte) error {
				// @todo check if k contains match_
				b := bucket.Bucket(k)
				b.ForEach(func(k, v []byte) error {
					var entry Entry
					if err := json.Unmarshal(v, &entry); err != nil {
						e.Logger.Error(err)
					} else {
						for k, _ := range entry.Data.LeafCert.Extensions {
							keys[k] = true
						}
					}
					return nil
				})
				return nil
			})
			return nil
		})
		return c.JSON(http.StatusOK, keys)
	})*/


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
	e.Logger.Fatal(e.Start(config.ServerPort))
}
