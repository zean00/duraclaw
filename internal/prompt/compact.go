package prompt

import "strings"

type Message struct {
	Role    string
	Content string
}

type CompactionResult struct {
	Messages       []Message
	Omitted        int
	OriginalChars  int
	CompactedChars int
}

func CompactMessages(messages []Message, maxChars int) CompactionResult {
	res := CompactionResult{OriginalChars: totalChars(messages)}
	if maxChars <= 0 || res.OriginalChars <= maxChars {
		res.Messages = append([]Message(nil), messages...)
		res.CompactedChars = res.OriginalChars
		return res
	}
	var kept []Message
	used := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		size := len(m.Role) + len(m.Content)
		if len(kept) > 0 {
			size++
		}
		if used+size > maxChars {
			res.Omitted = i + 1
			break
		}
		kept = append(kept, m)
		used += size
	}
	reverse(kept)
	if res.Omitted > 0 {
		prefix := Message{Role: "system", Content: "[Earlier conversation compacted: " + pluralizeTurns(res.Omitted) + " omitted.]"}
		kept = append([]Message{prefix}, kept...)
	}
	res.Messages = kept
	res.CompactedChars = totalChars(kept)
	return res
}

func totalChars(messages []Message) int {
	n := 0
	for _, m := range messages {
		n += len(m.Role) + len(m.Content)
	}
	return n
}

func reverse(messages []Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}

func pluralizeTurns(n int) string {
	var b strings.Builder
	b.WriteString(intString(n))
	if n == 1 {
		b.WriteString(" message")
	} else {
		b.WriteString(" messages")
	}
	return b.String()
}

func intString(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
