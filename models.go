package certmon

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


