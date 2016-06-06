package todow

import "time"

const (
	HTTPUser     = "todow"
	HTTPPassword = "todow"

	APIPath = "/api/"
)

type Item struct {
	ID      int64
	Body    string
	Created time.Time
	Done    bool
}
