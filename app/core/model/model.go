package model

import (
	"crypto/rand"
	"math/big"
	"time"
)

// RootTurnID is the virtual root of every session's turn tree.
const RootTurnID = "0"

const (
	StatusDone      = "done"
	StatusAborted   = "aborted"
	StatusStreaming = "streaming"
)

type Session struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	OwnerID   string `json:"ownerDeviceId"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// SyncState is local-only bookkeeping: local | synced.
	SyncState string `json:"syncState,omitempty"`
	// ServerID is the sessionsvr identity this session is synced to. Local only.
	ServerID string `json:"-"`
}

type Attachment struct {
	ID      string `json:"id,omitempty"`
	BlobID  string `json:"blobId,omitempty"`
	Name    string `json:"name"`
	MIME    string `json:"mime"`
	Size    int64  `json:"size"`
	IsImage bool   `json:"isImage,omitempty"`
	// FileID is the DeepSeek Files API id. Local only; never sent to sessionsvr.
	FileID string `json:"-"`
}

type Q struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   int64        `json:"createdAt"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	WebSearchRequests        int `json:"web_search_requests,omitempty"`
}

type Answer struct {
	Status     string           `json:"status"`
	StopReason string           `json:"stopReason,omitempty"`
	Blocks     []map[string]any `json:"blocks"`
	Usage      *Usage           `json:"usage,omitempty"`
	Model      string           `json:"model,omitempty"`
	Thinking   bool             `json:"thinking,omitempty"`
	Search     bool             `json:"search,omitempty"`
	// LatestReminder 是这一轮生效的 latest_reminder 快照（日期、星期、地点、平台、语言）。
	// 存下来是为了回放历史时用当时的快照，而不是用当前时间重算。
	LatestReminder string `json:"latestReminder,omitempty"`
	Title          string `json:"title,omitempty"`
	DurationMs     int64  `json:"durationMs,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
}

// Turn is one Q/A node in the tree. Answer is nil while the turn is open.
type Turn struct {
	ID           string  `json:"id"`
	SessionID    string  `json:"-"`
	ParentTurnID string  `json:"parentTurnId"`
	Q            Q       `json:"q"`
	CreatedAt    int64   `json:"createdAt"`
	Answer       *Answer `json:"answer,omitempty"`
}

func (t *Turn) Closed() bool { return t != nil && t.Answer != nil }

func NowMS() int64 { return time.Now().UnixMilli() }

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var base32 = big.NewInt(32)

// NewID returns a 26-char ULID (Crockford base32, time sortable).
func NewID() string {
	var id [16]byte
	ms := uint64(NowMS())
	id[0] = byte(ms >> 40)
	id[1] = byte(ms >> 32)
	id[2] = byte(ms >> 24)
	id[3] = byte(ms >> 16)
	id[4] = byte(ms >> 8)
	id[5] = byte(ms)
	_, _ = rand.Read(id[6:])

	n := new(big.Int).SetBytes(id[:])
	out := make([]byte, 26)
	mod := new(big.Int)
	for i := 25; i >= 0; i-- {
		n.DivMod(n, base32, mod)
		out[i] = crockford[mod.Int64()]
	}
	return string(out)
}
