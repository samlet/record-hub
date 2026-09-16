package identity

// PrincipalKind separates human and workload identities. The kind is selected
// by the verifier configuration, never inferred from optional token claims.
type PrincipalKind string

const (
	PrincipalUser    PrincipalKind = "user"
	PrincipalService PrincipalKind = "service"
)

// Principal is the authenticated identity passed to authorization code.
// Issuer and Subject together form its stable external identity.
type Principal struct {
	Kind              PrincipalKind
	Issuer            string
	Subject           string
	Audience          []string
	Email             string
	EmailVerified     bool
	Name              string
	PreferredUsername string
	Groups            []string
}

// IdentityKey is safe to use as a local membership lookup key.
type IdentityKey struct {
	Issuer  string `json:"issuer" bson:"issuer"`
	Subject string `json:"subject" bson:"subject"`
}

func (p Principal) IdentityKey() IdentityKey {
	return IdentityKey{Issuer: p.Issuer, Subject: p.Subject}
}
