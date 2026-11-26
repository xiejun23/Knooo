package sessions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// MessageRevision identifies an ordered transcript or a saved review's prefix.
func MessageRevision(messages []Message) string {
	hash := sha256.New()
	hash.Write([]byte("review-v1\n"))
	_ = json.NewEncoder(hash).Encode(messages)
	return hex.EncodeToString(hash.Sum(nil))
}
