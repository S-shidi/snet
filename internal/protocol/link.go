package protocol

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeCode removes grouping dashes and lowercases, returns uppercase.
func NormalizeCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", ""))
}

// BuildLink returns a snet:// link carrying the network id, pairing code and,
// when server is non-empty, the coordination server address the invite belongs
// to. A server-less link is still valid: the joiner targets its bound server.
// When name is non-empty, it is included as a display hint for the joiner.
func BuildLink(nid, code, server string, name string) string {
	link := "snet://join?nid=" + url.QueryEscape(nid) + "&code=" + url.QueryEscape(NormalizeCode(code))
	if name != "" {
		link += "&name=" + url.QueryEscape(name)
	}
	if server != "" {
		link += "&server=" + url.QueryEscape(server)
	}
	return link
}

// ParseLink extracts nid, code, an optional server and an optional name from a
// snet:// link, an http(s) join URL, or a bare "nid:code" string. server and
// name are empty for links that omit them.
func ParseLink(s string) (nid, code, server, name string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", "", "", fmt.Errorf("empty input")
	}
	if u, e := url.Parse(s); e == nil && (u.Scheme == "snet" || u.Scheme == "http" || u.Scheme == "https") {
		q := u.Query()
		nid = q.Get("nid")
		code = NormalizeCode(q.Get("code"))
		if nid == "" || code == "" {
			return "", "", "", "", fmt.Errorf("invalid link: missing nid or code")
		}
		return nid, code, q.Get("server"), q.Get("name"), nil
	}
	if i := strings.IndexByte(s, ':'); i > 0 {
		return s[:i], NormalizeCode(s[i+1:]), "", "", nil
	}
	return "", "", "", "", fmt.Errorf("invalid link format %q", s)
}
