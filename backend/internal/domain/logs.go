package domain

const MaxDriverLogLines = 10000
const MaxDriverLogBytes = 4 << 20

type DriverLogQuery struct {
	Source    string
	Mode      string
	StartLine int
	EndLine   int
	Limit     int
	From      string
	To        string
	Direction string
}

type DriverLogWindow struct {
	Source    string   `json:"source"`
	Mode      string   `json:"mode"`
	Lines     []string `json:"lines"`
	StartLine int      `json:"startLine"`
	EndLine   int      `json:"endLine"`
	HasMore   bool     `json:"hasMore"`
	ByteLimit bool     `json:"byteLimit"`
	Limit     int      `json:"limit"`
}
