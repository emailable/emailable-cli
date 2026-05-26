package output

// AccountView is the account summary: owner email plus available credits.
//
// Fields are flattened (rather than embedding the source struct) to keep the
// JSON shape an explicit, stable contract for downstream consumers — embedding
// would nest or distort the output since encoding/json has no "inline" tag.
type AccountView struct {
	OwnerEmail       string `json:"owner_email"`
	AvailableCredits int    `json:"available_credits"`
}
