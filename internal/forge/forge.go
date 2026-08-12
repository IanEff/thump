package forge

// Release is one corrective maintenance release: the changed source and the
// notes a reviewer reads before accepting it.
type Release struct {
	Key     string // open release per authored contract ref
	Path    string // the file in the GitOps source this release changes.
	Content []byte
	Notes   string
}
