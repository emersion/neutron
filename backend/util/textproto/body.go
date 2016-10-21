package textproto

import (
	"io"
	"strings"
	"strconv"
)

type BodyStructure struct {
	ID string
	Type string
	SubType string
	Params map[string]string
	ContentId string
	ContentDescription string
	ContentEncoding string
	Content io.Reader
	Size int
	Children []*BodyStructure
}

func (s *BodyStructure) Get(id string) *BodyStructure {
	if id == "" {
		return s
	}

	parts := strings.SplitN(id, ".", 2)
	index, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil
	}

	var childId string
	if len(parts) == 2 {
		childId = parts[1]
	}

	for i, child := range s.Children {
		if i == index - 1 {
			return child.Get(childId)
		}
	}

	return nil
}

func Decode(r io.Reader, encoding, charset string) io.Reader {
	if encoding != "" {
		r = decodeContentEncoding(r, encoding)
	}

	if charset != "" {
		r = decodeCharset(r, charset)
	}

	return r
}
