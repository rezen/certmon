package certmon

type Config struct {
	Stream        string
	Storage       string // redis,bolt
	RedisHost     string
	RedisPort     string
	RedisPassword string
	LogLevel      string
	ServerPort    string
	BoltPath      string
	// Pruner config
}
