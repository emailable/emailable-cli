package api

// rawReceiver is implemented by response types that cache the verbatim JSON body
// so machine output can pass it through unchanged (re-encoding drops nulls / unknown fields).
type rawReceiver interface {
	setRaw([]byte)
}

// rawJSON holds the captured response body; the field is unexported so
// encoding/json never round-trips it back into output.
type rawJSON struct {
	raw []byte
}

func (r *rawJSON) setRaw(b []byte) { r.raw = b }

func (r *rawJSON) RawJSON() []byte { return r.raw }

// VerifyResult is the response from GET /verify.
type VerifyResult struct {
	rawJSON
	Email        string  `json:"email"`
	State        string  `json:"state"` // deliverable, undeliverable, risky, unknown
	Reason       string  `json:"reason"`
	Score        int     `json:"score"`
	Domain       string  `json:"domain"`
	Disposable   bool    `json:"disposable"`
	AcceptAll    bool    `json:"accept_all"`
	Role         bool    `json:"role"`
	Free         bool    `json:"free"`
	MailboxFull  bool    `json:"mailbox_full"`
	NoReply      bool    `json:"no_reply"`
	MXRecord     string  `json:"mx_record,omitempty"`
	SMTPProvider string  `json:"smtp_provider,omitempty"`
	DidYouMean   string  `json:"did_you_mean,omitempty"`
	User         string  `json:"user,omitempty"`
	FirstName    string  `json:"first_name,omitempty"`
	LastName     string  `json:"last_name,omitempty"`
	FullName     string  `json:"full_name,omitempty"`
	Gender       string  `json:"gender,omitempty"`
	Tag          string  `json:"tag,omitempty"`
	Duration     float64 `json:"duration,omitempty"`
}

// BatchSubmit is the response from POST /batch when a batch is created.
type BatchSubmit struct {
	rawJSON
	ID      string `json:"id"`
	Message string `json:"message"`
}

// BatchTotalCounts holds aggregate progress counts from a partial batch response.
type BatchTotalCounts struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
}

// BatchStatus merges three API payload shapes: in-progress (Total/Processed),
// completed small batch (Emails), completed large batch (DownloadFile), and
// partial snapshot (TotalCounts — top-level Total/Processed are NOT set).
type BatchStatus struct {
	rawJSON
	ID           string            `json:"id,omitempty"`
	Total        int               `json:"total"`
	Processed    int               `json:"processed"`
	Status       string            `json:"status,omitempty"`
	Message      string            `json:"message,omitempty"`
	Reason       map[string]int    `json:"reason_counts,omitempty"`
	Emails       []VerifyResult    `json:"emails,omitempty"`
	DownloadFile string            `json:"download_file,omitempty"`
	TotalCounts  *BatchTotalCounts `json:"total_counts,omitempty"`
}

// IsComplete checks TotalCounts before top-level counts: partial-results payloads
// omit the top-level counts, which would otherwise look complete prematurely.
func (b *BatchStatus) IsComplete() bool {
	if b.DownloadFile != "" {
		return true
	}
	if b.TotalCounts != nil {
		return b.TotalCounts.Total > 0 && b.TotalCounts.Processed >= b.TotalCounts.Total
	}
	if b.Total > 0 && b.Processed >= b.Total {
		return true
	}
	if b.Total == 0 && len(b.Emails) > 0 {
		return true
	}
	return false
}

// Progress returns the processed and total counts for b, and ok=false when progress is unknown.
func (b *BatchStatus) Progress() (processed, total int, ok bool) {
	if b.TotalCounts != nil {
		return b.TotalCounts.Processed, b.TotalCounts.Total, true
	}
	if b.Total > 0 {
		return b.Processed, b.Total, true
	}
	return 0, 0, false
}

// Account is the response from GET /account.
type Account struct {
	rawJSON
	OwnerEmail       string `json:"owner_email"`
	AvailableCredits int    `json:"available_credits"`
}
