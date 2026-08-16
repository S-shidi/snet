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

// BuildLink returns a vnet:// link carrying the network id, pairing code and,
// when server is non-empty, the coordination server address the invite belongs
// to. A server-less link is still valid: the joiner targets its bound server.
func BuildLink(nid, code, server string) string {
	link := "vnet://join?nid=" + url.QueryEscape(nid) + "&code=" + url.QueryEscape(NormalizeCode(code))
	if server != "" {
		link += "&server=" + url.QueryEscape(server)
	}
	return link
}

// ParseLink extracts nid, code and an optional server from a vnet:// link, an
// http(s) join URL, or a bare "nid:code" string. server is empty for links
// that predate the server parameter or that omit it.
func ParseLink(s string) (nid, code, server string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", "", fmt.Errorf("empty input")
	}
	if u, e := url.Parse(s); e == nil && (u.Scheme == "vnet" || u.Scheme == "http" || u.Scheme == "https") {
		q := u.Query()
		nid = q.Get("nid")
		code = NormalizeCode(q.Get("code"))
		if nid == "" || code == "" {
			return "", "", "", fmt.Errorf("invalid link: missing nid or code")
		}
		return nid, code, q.Get("server"), nil
	}
	if i := strings.IndexByte(s, ':'); i > 0 {
		return s[:i], NormalizeCode(s[i+1:]), "", nil
	}
	return "", "", "", fmt.Errorf("invalid link format %q", s)
}
