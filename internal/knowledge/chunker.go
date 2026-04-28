package knowledge

import "strings"

type Chunk struct {
	Index   int
	Content string
}

func ChunkText(text string, maxChars int) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxChars <= 0 {
		maxChars = 1200
	}
	paragraphs := strings.Split(text, "\n\n")
	var chunks []Chunk
	var current strings.Builder
	flush := func() {
		content := strings.TrimSpace(current.String())
		if content != "" {
			chunks = append(chunks, Chunk{Index: len(chunks), Content: content})
		}
		current.Reset()
	}
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if current.Len() > 0 && current.Len()+2+len(para) > maxChars {
			flush()
		}
		for len(para) > maxChars {
			if current.Len() > 0 {
				flush()
			}
			chunks = append(chunks, Chunk{Index: len(chunks), Content: strings.TrimSpace(para[:maxChars])})
			para = strings.TrimSpace(para[maxChars:])
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}
	flush()
	return chunks
}
