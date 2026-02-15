package httpx

import (
	"net/http"
	"strings"
)

// parseDeviceName parses the User-Agent header into a friendly display name.
func parseDeviceName(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		return "Web Session (Unknown Device)"
	}

	os := "Unknown OS"
	if strings.Contains(ua, "Windows") {
		os = "Windows"
	} else if strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS") {
		os = "macOS"
	} else if strings.Contains(ua, "Android") {
		os = "Android"
	} else if strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") {
		os = "iOS"
	} else if strings.Contains(ua, "Linux") {
		os = "Linux"
	}

	browser := "Web Browser"
	if strings.Contains(ua, "Edg/") {
		browser = "Microsoft Edge"
	} else if strings.Contains(ua, "Chrome/") {
		browser = "Chrome Browser"
	} else if strings.Contains(ua, "Firefox/") {
		browser = "Firefox"
	} else if strings.Contains(ua, "Safari/") && !strings.Contains(ua, "Chrome/") {
		browser = "Safari"
	}

	return browser + " on " + os
}

// getClientIP extracts the real client IP address from request headers or RemoteAddr.
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	if ip == "[" || ip == "]" || ip == "" {
		return "127.0.0.1"
	}
	return ip
}
