package textproto

import (
	"encoding/base64"
	"mime/quotedprintable"
	"strings"
	"io"
	"log"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

func decodeCharset(r io.Reader, charset string) io.Reader {
	var enc encoding.Encoding
	switch strings.ToLower(charset) {
	case "iso-8859-1":
		enc = charmap.ISO8859_1
	case "windows-1252":
		enc = charmap.Windows1252
	case "utf-8", "us-ascii":
		// Nothing to do
	default:
		if charset != "" {
			log.Println("WARN: unsupported charset:", charset)
		}
	}
	if enc != nil {
		r = enc.NewDecoder().Reader(r)
	}
	return r
}

func decodeContentEncoding(r io.Reader, contentEncoding string) io.Reader {
	switch strings.ToLower(contentEncoding) {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "7bit", "8bit", "binary":
		// Nothing to do
	default:
		if contentEncoding != "" {
			log.Println("WARN: unsupported content encoding:", contentEncoding)
		}
	}
	return r
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
