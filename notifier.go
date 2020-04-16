package certmon

import (
	"github.com/go-redis/redis"
	"github.com/labstack/echo"
	"os"
)

type RedisNotifier struct {
	Client  *redis.Client
	Channel string
	Logger  echo.Logger
}

func (r *RedisNotifier) Notify(m Match) {
	r.Client.Publish(r.Channel, m.EntryString)
}

type Notifier interface {
	Notify(m Match)
}

type LogNotifier struct {
	Path   string
	Logger echo.Logger
}

func (l *LogNotifier) Notify(m Match) {
	file, err := os.OpenFile(l.Path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		l.Logger.Error(err)
	}
	defer file.Close()
	_, err = file.WriteString(m.EntryString + "\n")
	if err != nil {
		l.Logger.Error(err)
	}
}
