package main

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Все размеры Zep считает в символах, а не байтах: для кириллицы разница
// двукратная, поэтому длины и срезы здесь только по рунам.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

func runeCut(s string, n int) (head, tail string) {
	if runeLen(s) <= n {
		return s, ""
	}
	r := []rune(s)
	return string(r[:n]), string(r[n:])
}

func runeTail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

type textChunk struct {
	text    string
	heading string
}

var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

// chunkMarkdown режет текст по абзацам, держа цепочку markdown-заголовков,
// чтобы каждый чанк уносил с собой свой раздел.
func chunkMarkdown(text string, size, overlap int) []textChunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var out []textChunk
	cur := ""
	trail := make([]string, 7) // trail[level] — последний заголовок этого уровня
	carry := 0                 // сколько в буфере занимает хвост-перекрытие от прошлого чанка

	flush := func(heading string) {
		s := strings.TrimSpace(cur)
		cur, carry = "", 0
		if s == "" {
			return
		}
		out = append(out, textChunk{text: s, heading: heading})
		if overlap > 0 && runeLen(s) > overlap {
			tail := trimToWord(runeTail(s, overlap)) + "\n\n"
			cur = tail
			carry = runeLen(tail)
		}
	}

	for _, block := range strings.Split(text, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if m := headingRe.FindStringSubmatch(strings.SplitN(block, "\n", 2)[0]); m != nil {
			// Заголовок закрывает предыдущий раздел, но пустой чанк из одного
			// хвоста-перекрытия плодить не надо.
			if runeLen(cur) > carry {
				flush(headingPath(trail))
			}
			level := len(m[1])
			trail[level] = strings.TrimSpace(m[2])
			for i := level + 1; i < len(trail); i++ {
				trail[i] = ""
			}
		}

		// makeRoom освобождает место под need рун: сначала закрывает чанк, а если
		// мешает уже только хвост-перекрытие — жертвует и им, иначе чанк вылезет за size.
		makeRoom := func(need int) {
			if runeLen(cur)+need <= size {
				return
			}
			if cur != "" {
				flush(headingPath(trail))
			}
			if runeLen(cur)+need > size {
				cur, carry = "", 0
			}
		}

		if runeLen(block) > size {
			for _, piece := range splitLong(block, size) {
				makeRoom(runeLen(piece) + 1)
				if cur != "" {
					cur += " "
				}
				cur += piece
			}
			continue
		}

		makeRoom(runeLen(block) + 2)

		if cur != "" {
			cur += "\n\n"
		}
		cur += block
	}
	flush(headingPath(trail))

	return out
}

var sentenceRe = regexp.MustCompile(`(?s)(.+?[.!?…](?:\s+|$))`)

func splitLong(block string, size int) []string {
	sentences := sentenceRe.FindAllString(block, -1)
	if len(sentences) == 0 {
		sentences = []string{block}
	}
	var out []string
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		for runeLen(s) > size {
			head, tail := runeCut(s, size)
			// Стараемся резать по границе слова, но не откусывая больше половины.
			if idx := strings.LastIndex(head, " "); idx > 0 && runeLen(head[:idx]) > size/2 {
				tail = head[idx+1:] + tail
				head = head[:idx]
			}
			out = append(out, strings.TrimSpace(head))
			s = strings.TrimSpace(tail)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func trimToWord(s string) string {
	if idx := strings.IndexAny(s, " \n"); idx > 0 && idx < len(s)-1 {
		return strings.TrimSpace(s[idx+1:])
	}
	return strings.TrimSpace(s)
}

func headingPath(trail []string) string {
	var parts []string
	for _, h := range trail {
		if h != "" {
			parts = append(parts, h)
		}
	}
	return strings.Join(parts, " › ")
}
