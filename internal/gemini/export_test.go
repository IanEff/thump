package gemini

import (
	"github.com/ianeff/thump/internal/reason"
	"google.golang.org/genai"
)

// ToGeminiContentsForTest exposes toGeminiContents to gemini_test — the
// Message-to-wire render, independent of a live GenerateContent call.
func ToGeminiContentsForTest(msgs []reason.Message) []*genai.Content {
	m := &Model{thoughtSigs: make(map[string][]byte)}
	return m.toGeminiContents(msgs)
}
