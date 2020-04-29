package certmon

import (
	"github.com/boltdb/bolt"
	"github.com/go-redis/redis"
	tld "github.com/jpillora/go-tld"
	"github.com/labstack/echo"
	"time"
)

type RedisStorage struct {
	Client  *redis.Client
	Counter *Counter
}

func (r *RedisStorage) Monitor(domain string) error {
	r.Client.HSet("watch_domains", domain, "1")
	return nil
}

func (r *RedisStorage) Domains() []string {
	domains, err := r.Client.HKeys("watch_domains").Result()
	if err != nil {
		return []string{}
	}
	return domains
}

func (r *RedisStorage) Matches(domain string) []Entry {
	return []Entry{}
}

func (r *RedisStorage) Remove(domain string) error {
	r.Client.HDel("watch_domains", domain).Result()
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
	tx, err := s.DB.Begin(true)
	if err != nil {
		return domains
	}
	defer tx.Rollback()
	bucket, err := tx.CreateBucketIfNotExists([]byte(s.BucketName))

	if err != nil {
		return domains
	}
	
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
	tx, err := s.DB.Begin(true)
	if err != nil {
		s.Counter.Increment("bolt_errors")
		return false, err
	}
	defer tx.Rollback()
	bucket, err := tx.CreateBucketIfNotExists([]byte(s.BucketName))

	if err != nil {
		panic(err)
	}
	v := bucket.Get([]byte(entry.Domain))
	return string(v) == "1", err
}
