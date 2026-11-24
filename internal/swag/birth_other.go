//go:build !darwin

package swag

import (
	"os"
	"time"
)

func birthTime(fi os.FileInfo) time.Time { return time.Time{} }
