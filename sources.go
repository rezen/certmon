package certmon

import (
	"time"
	"github.com/pkg/errors"
		"github.com/gorilla/websocket"
)

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

