package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const cyrillicDoc = `# Заголовок документа

Первый абзац про долговременную память кодинг-агентов. Он достаточно длинный, чтобы чанкер
успел набрать буфер и решить, где резать текст на границе предложения, а не посреди слова.

## Раздел про Zep

Второй абзац описывает Batch API: draft-батч, добавление items и запуск обработки. Zep считает
лимиты в символах, а не в байтах, поэтому кириллица не должна ужимать чанки вдвое.

### Подраздел

Третий абзац.`

func TestChunkMarkdownRespectsRuneSize(t *testing.T) {
	const size = 200
	chunks := chunkMarkdown(cyrillicDoc, size, 20)
	if len(chunks) < 2 {
		t.Fatalf("ожидалось несколько чанков, получено %d", len(chunks))
	}
	for i, c := range chunks {
		if got := utf8.RuneCountInString(c.text); got > size {
			t.Errorf("чанк %d длиной %d рун превышает лимит %d", i, got, size)
		}
		if !utf8.ValidString(c.text) {
			t.Errorf("чанк %d — битый UTF-8", i)
		}
	}
	// Байтовый счёт дал бы вдвое больше чанков на кириллице.
	if utf8.RuneCountInString(cyrillicDoc)/size > len(chunks) {
		t.Errorf("чанков %d — меньше, чем требует объём текста", len(chunks))
	}
}

func TestChunkMarkdownCarriesHeadingPath(t *testing.T) {
	chunks := chunkMarkdown(cyrillicDoc, 200, 20)
	var seen bool
	for _, c := range chunks {
		if strings.Contains(c.heading, "Раздел про Zep") {
			seen = true
			if !strings.HasPrefix(c.heading, "Заголовок документа") {
				t.Errorf("цепочка заголовков потеряла верхний уровень: %q", c.heading)
			}
		}
	}
	if !seen {
		t.Error("ни один чанк не получил заголовок раздела")
	}
}

func TestRuneCutKeepsValidUTF8(t *testing.T) {
	data := "[docs/файл.md › Заголовок]\n\n" + strings.Repeat("я", 50)
	head, tail := runeCut(data, 20)
	if utf8.RuneCountInString(head) != 20 {
		t.Errorf("head = %d рун, ожидалось 20", utf8.RuneCountInString(head))
	}
	if !utf8.ValidString(head) || !utf8.ValidString(tail) {
		t.Error("runeCut порвал UTF-8")
	}
	if head+tail != data {
		t.Error("runeCut потерял данные")
	}
}

func TestSplitLongNeverExceedsSize(t *testing.T) {
	long := strings.Repeat("длинное предложение без точки ", 40)
	for _, piece := range splitLong(long, 100) {
		if got := utf8.RuneCountInString(piece); got > 100 {
			t.Errorf("кусок длиной %d рун превышает 100", got)
		}
	}
}
