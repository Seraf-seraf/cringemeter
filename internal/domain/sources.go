package domain

import (
	"net/url"
	"strings"
)

type sourceDomain struct {
	host  string
	parse func(*url.URL) string
}

var supportedDomains = [...]sourceDomain{
	{"youtu.be", youtubeShortID},
	{"www.youtu.be", youtubeShortID},
	{"youtube.com", youtubeID},
	{"www.youtube.com", youtubeID},
	{"m.youtube.com", youtubeID},
	{"music.youtube.com", youtubeID},
	{"instagram.com", instagramID},
	{"www.instagram.com", instagramID},
	{"m.instagram.com", instagramID},
	{"tiktok.com", tiktokID},
	{"www.tiktok.com", tiktokID},
	{"m.tiktok.com", tiktokID},
	{"vm.tiktok.com", tiktokShareID},
	{"vt.tiktok.com", tiktokShareID},
}

func SourceVideoID(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	for _, entry := range supportedDomains {
		if strings.EqualFold(u.Hostname(), entry.host) {
			id := entry.parse(u)
			if ValidSourceID(id) {
				return id
			}
			return ""
		}
	}
	return ""
}

func ValidSourceID(id string) bool {
	if ValidYouTubeID(id) {
		return true
	}
	provider, value, ok := strings.Cut(id, ":")
	if !ok {
		return false
	}
	switch provider {
	case "instagram", "tiktok-share":
		return sourceToken(value)
	case "tiktok":
		if len(value) == 0 || len(value) > 25 {
			return false
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func sourceToken(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func sourcePath(u *url.URL) []string {
	return strings.Split(strings.Trim(u.Path, "/"), "/")
}

func youtubeShortID(u *url.URL) string {
	parts := sourcePath(u)
	if len(parts) == 1 && ValidYouTubeID(parts[0]) {
		return parts[0]
	}
	return ""
}

func youtubeID(u *url.URL) string {
	parts := sourcePath(u)
	if len(parts) == 1 && parts[0] == "watch" {
		if id := u.Query().Get("v"); ValidYouTubeID(id) {
			return id
		}
	}
	if len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "embed" || parts[0] == "live") && ValidYouTubeID(parts[1]) {
		return parts[1]
	}
	return ""
}

func instagramID(u *url.URL) string {
	parts := sourcePath(u)
	if len(parts) == 2 && (parts[0] == "reel" || parts[0] == "reels" || parts[0] == "p" || parts[0] == "tv") {
		return "instagram:" + parts[1]
	}
	return ""
}

func tiktokID(u *url.URL) string {
	parts := sourcePath(u)
	if len(parts) == 3 && strings.HasPrefix(parts[0], "@") && len(parts[0]) > 1 && parts[1] == "video" {
		return "tiktok:" + parts[2]
	}
	if len(parts) == 2 && parts[0] == "t" {
		return "tiktok-share:" + parts[1]
	}
	return ""
}

func tiktokShareID(u *url.URL) string {
	parts := sourcePath(u)
	if len(parts) == 1 {
		return "tiktok-share:" + parts[0]
	}
	return ""
}
