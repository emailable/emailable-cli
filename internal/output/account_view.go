package output

// AccountView is what `emailable account status` prints: owner email plus
// available credits.
//
// We flatten the api.Account fields here (rather than embedding *api.Account)
// because encoding/json has no "inline" tag — embedding would either nest the
// data under a key or produce odd output. Keeping the JSON shape explicit
// also means downstream consumers (scripts, agents) see a stable contract.
type AccountView struct {
	OwnerEmail       string `json:"owner_email"`
	AvailableCredits int    `json:"available_credits"`
}
