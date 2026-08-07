// Package evidence holds the read-only tools the reason loop investigates
// with. Every tool returns a proposal.EvidenceRef — a digest, the query that
// produced it, and a pointer to re-fetch the source — never the payload
// itself, so no raw backend data can enter the model's conversation. A tool
// that could mutate what it reads does not belong here.
package evidence
