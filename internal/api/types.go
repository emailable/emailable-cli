package api

// rawReceiver is implemented by response types that retain the verbatim JSON
// body the server returned. Client.do calls setRaw after a successful decode so
// machine output can pass the original bytes through unchanged.
type rawReceiver interface {
	setRaw([]byte)
}

// rawJSON holds a captured response body. Embedding it gives a type the raw
// passthrough plumbing for free. The field is unexported so encoding/json
// never round-trips it back into output.
type rawJSON struct {
	raw []byte
}

func (r *rawJSON) setRaw(b []byte) { r.raw = b }

// RawJSON returns the verbatim response body the server sent, or nil if the
// value wasn't produced by an API call (e.g. constructed in a test). Callers
// emitting machine output should prefer these bytes over re-encoding.
func (r *rawJSON) RawJSON() []byte { return r.raw }

// VerifyResult is the response from GET /v1/verify.
//
// The typed fields below drive human/CSV rendering. JSON output is served from
// the captured raw body (see rawJSON), so nullable fields and any field this
// struct doesn't model still pass through unchanged.
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

// BatchSubmit is the response from POST /v1/batch.
type BatchSubmit struct {
	rawJSON
	ID      string `json:"id"`
	Message string `json:"message"`
}

// BatchTotalCounts is the `total_counts` object returned alongside a
// partial-results payload. Only Total and Processed are load-bearing for
// progress display.
type BatchTotalCounts struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
}

// BatchStatus is the response from GET /v1/batch.
//
// The API returns three distinct payload shapes that this struct merges:
//
//   - In-progress (partial=false): top-level Total + Processed counts, no
//     Emails, no DownloadFile.
//   - Completed small batch (≤1000): Emails slice populated, Total/Processed
//     dropped from the payload.
//   - Completed large batch (>1000): DownloadFile URL only.
//   - Partial snapshot (partial=true): Emails contains the rows ready so far,
//     a top-level Message describes the partial state, and progress lives
//     under TotalCounts (the top-level Total/Processed are NOT used).
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

// IsComplete reports whether the batch has finished processing.
//
// TotalCounts is checked before the top-level counts because a partial-results
// payload omits the latter and would otherwise fall through to the
// Emails-populated branch and look complete prematurely.
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

// Progress returns (processed, total, ok). ok is false when the payload
// carries no progress counters (e.g. a completed small-batch payload that
// dropped them).
func (b *BatchStatus) Progress() (processed, total int, ok bool) {
	if b.TotalCounts != nil {
		return b.TotalCounts.Processed, b.TotalCounts.Total, true
	}
	if b.Total > 0 {
		return b.Processed, b.Total, true
	}
	return 0, 0, false
}

// Account is the response from GET /v1/account.
type Account struct {
	rawJSON
	OwnerEmail       string `json:"owner_email"`
	AvailableCredits int    `json:"available_credits"`
}
