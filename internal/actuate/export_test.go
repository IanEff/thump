package actuate

import "github.com/ianeff/thump/internal/contract"

// NewWith exposes the fake-Kube constructor to the external test package
// without widening the production surface (production callers get New,
// which builds a real in-cluster client).
func NewWith(k Kube, cat *contract.StaticCatalog) (*Runner, error) { return newWith(k, cat) }
