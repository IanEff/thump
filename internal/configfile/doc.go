// Package configfile is the shared ritual behind every staged-YAML config
// loader in this tree: read the file into a pointer-fielded staging struct,
// then require each field present before building the real value. A staged
// field left nil is an omitted key, distinguishable from an explicit zero —
// the property every LoadXFile in this repo fails at load rather than at
// first use to preserve.
package configfile
