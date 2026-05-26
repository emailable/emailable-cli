package api

import "testing"

func TestBatchStatus_IsComplete(t *testing.T) {
	tests := []struct {
		name string
		bs   BatchStatus
		want bool
	}{
		{
			name: "fresh / queued: no counts, no emails",
			bs:   BatchStatus{},
			want: false,
		},
		{
			name: "processing: counts present, not yet done",
			bs:   BatchStatus{Total: 100, Processed: 42},
			want: false,
		},
		{
			name: "counts say done",
			bs:   BatchStatus{Total: 100, Processed: 100},
			want: true,
		},
		{
			name: "counts overshot (paranoia)",
			bs:   BatchStatus{Total: 100, Processed: 101},
			want: true,
		},
		{
			name: "API completion payload: counts absent, emails populated",
			bs: BatchStatus{
				Emails: []VerifyResult{{Email: "a@b.com"}},
			},
			want: true,
		},
		{
			name: "large batch: DownloadFile signals done",
			bs:   BatchStatus{DownloadFile: "https://example.com/results.zip"},
			want: true,
		},
		{
			name: "processing with partial emails: total > 0 wins over emails",
			bs: BatchStatus{
				Total:     100,
				Processed: 3,
				Emails:    []VerifyResult{{Email: "a@b.com"}, {Email: "c@d.com"}, {Email: "e@f.com"}},
			},
			want: false,
		},
		{
			name: "partial=true payload, in flight: total_counts present and Processed < Total",
			bs: BatchStatus{
				TotalCounts: &BatchTotalCounts{Processed: 5, Total: 100},
				Emails:      []VerifyResult{{Email: "a@b.com"}},
			},
			want: false,
		},
		{
			name: "partial=true payload, complete: total_counts says done",
			bs: BatchStatus{
				TotalCounts: &BatchTotalCounts{Processed: 100, Total: 100},
				Emails:      []VerifyResult{{Email: "a@b.com"}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.bs.IsComplete()
			if got != tt.want {
				t.Errorf("IsComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBatchStatus_Progress(t *testing.T) {
	tests := []struct {
		name      string
		bs        BatchStatus
		wantProc  int
		wantTotal int
		wantOK    bool
	}{
		{
			name:   "empty: no progress info",
			bs:     BatchStatus{},
			wantOK: false,
		},
		{
			name:      "top-level counts",
			bs:        BatchStatus{Total: 100, Processed: 25},
			wantProc:  25,
			wantTotal: 100,
			wantOK:    true,
		},
		{
			name:      "total_counts takes precedence",
			bs:        BatchStatus{Total: 999, Processed: 0, TotalCounts: &BatchTotalCounts{Processed: 7, Total: 50}},
			wantProc:  7,
			wantTotal: 50,
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProc, gotTotal, gotOK := tt.bs.Progress()
			if gotOK != tt.wantOK || (gotOK && (gotProc != tt.wantProc || gotTotal != tt.wantTotal)) {
				t.Errorf("Progress() = (%d, %d, %v), want (%d, %d, %v)",
					gotProc, gotTotal, gotOK, tt.wantProc, tt.wantTotal, tt.wantOK)
			}
		})
	}
}
