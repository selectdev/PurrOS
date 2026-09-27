// Package ids generates prefixed, time-sortable identifiers such as
// "emp_01J8ZQ7M3K2N4P5R6S7T8V9W0X".
package ids

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Prefixes for each entity type. IDs are opaque to API clients, but prefixes
// make them self-describing in logs and payloads.
const (
	Company         = "cmp"
	OrgUnit         = "org"
	Location        = "loc"
	Department      = "dep"
	Employee        = "emp"
	User            = "usr"
	Role            = "rol"
	Integration     = "int"
	IntegrationLog  = "ilg"
	APIKey          = "key"
	Audit           = "aud"
	Event           = "evt"
	WebhookEndpoint = "whe"
	WebhookDelivery = "whd"
	Batch           = "bat"
	Punch           = "pch"
	Item            = "itm"
	SalesTxn        = "stx"
	SalesSummary    = "ssm"
	Request         = "req"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New returns a new ID with the given prefix.
func New(prefix string) string {
	mu.Lock()
	id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy)
	mu.Unlock()
	return prefix + "_" + id.String()
}

// HasPrefix reports whether id looks like an ID of the given type.
func HasPrefix(id, prefix string) bool {
	return strings.HasPrefix(id, prefix+"_")
}
