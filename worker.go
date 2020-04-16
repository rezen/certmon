package certmon

import (
	"time"
	"github.com/labstack/echo"

)


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
		case <-ticker.C:
			l.Info(count)

		}
	}
}
