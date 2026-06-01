package output

// AccountView holds the account summary fields. Flattened rather than
// embedding the source struct — encoding/json has no "inline" tag and
// embedding would nest or distort the JSON shape.
type AccountView struct {
	OwnerEmail       string `json:"owner_email"`
	AvailableCredits int    `json:"available_credits"`
}
