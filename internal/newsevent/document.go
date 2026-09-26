package newsevent

import (
	"strings"

	"github.com/sperano/puckdb/internal/news"
)

// instructionPhrases are words aimed at a language model rather than a
// reader. An article sentence containing one is treated as a suspected
// injection: the extraction goes to review, and no event may rest on a quote
// from that sentence.
var instructionPhrases = []string{
	"ignore previous", "ignore all previous", "ignore the previous", "ignore prior", "ignore the above",
	"ignore your instructions", "ignore these instructions", "disregard previous", "disregard the above",
	"disregard all", "disregard your", "new instructions", "system prompt", "system message",
	"developer message", "you are an ai", "language model", "ai model", "as an ai", "respond with",
	"output the following", "reply with", "call the tool", "use the tool", "function call", "jailbreak",
}

// sentenceEnds split an article into sentences for injection checks.
const sentenceEnds = ".!?\n"

// docIndex is a document's words, with the sentence each word belongs to and
// which sentences look like instructions to a model.
type docIndex struct {
	doc        Document
	words      []string
	sentence   []int
	suspicious map[int]bool
}

func indexDocument(d Document) docIndex {
	idx := docIndex{doc: d, suspicious: make(map[int]bool)}
	text := d.Title + "\n" + d.Text
	sentences := strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(sentenceEnds, r) })
	for s, sentence := range sentences {
		words := news.Words(sentence)
		for _, w := range words {
			idx.words = append(idx.words, w)
			idx.sentence = append(idx.sentence, s)
		}
		for _, phrase := range instructionPhrases {
			if containsWords(words, news.Words(phrase)) {
				idx.suspicious[s] = true
				break
			}
		}
	}
	return idx
}

// find reports whether quote appears in the document, and whether every
// place it appears touches a suspected instruction.
func (idx docIndex) find(quote string) (found, suspicious bool) {
	needle := news.Words(quote)
	if len(needle) == 0 {
		return false, false
	}
	suspicious = true
	for i := 0; i+len(needle) <= len(idx.words); i++ {
		if !wordsEqualAt(idx.words, i, needle) {
			continue
		}
		found = true
		if !idx.touchesSuspicious(i, i+len(needle)) {
			suspicious = false
		}
	}
	return found, found && suspicious
}

func wordsEqualAt(words []string, at int, needle []string) bool {
	for j, w := range needle {
		if words[at+j] != w {
			return false
		}
	}
	return true
}

func (idx docIndex) touchesSuspicious(from, to int) bool {
	for i := from; i < to; i++ {
		if idx.suspicious[idx.sentence[i]] {
			return true
		}
	}
	return false
}

// hasInstructions reports whether any sentence looks like instructions.
func (idx docIndex) hasInstructions() bool {
	return len(idx.suspicious) > 0
}

// mentions reports whether the document's words contain phrase.
func (idx docIndex) mentions(phrase string) bool {
	return containsWords(idx.words, news.Words(phrase))
}
