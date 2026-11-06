package panel

import (
	"strings"

	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

// developerAllows applies a developer's areas to one request, the way a
// limited API token's areas apply (both can apply at once). On top of the
// areas, developers:
//   - may read their accounts (GET /accounts, /accounts/{a}) but never change them;
//   - work on existing websites: they can't create or delete one;
//   - manage their own sign-in (password, two-factor, API tokens);
//   - never reach billing, people, packages or the server.
func developerAllows(p *store.Person, method, apiPath string) error {
	if p.Role != store.Developer {
		return nil
	}
	path := strings.TrimPrefix(strings.TrimPrefix(apiPath, "/api"), "/v1")
	seg := strings.Split(strings.Trim(path, "/"), "/")
	area := areaOf(apiPath)
	switch area {
	case "":
		return nil
	case "tokens": // their own sign-in and tokens; never someone else's branding
		if len(seg) >= 2 && seg[0] == "me" && seg[1] == "branding" && method != "GET" {
			return denied("developers can't change branding")
		}
		return nil
	case "accounts":
		if method == "GET" && seg[0] == "accounts" && len(seg) <= 2 {
			return nil
		}
		return denied("developers can't change hosting accounts, people or plans; ask the account owner")
	case "sites":
		if seg[0] == "sites" && ((len(seg) == 1 && method == "POST") || (len(seg) == 2 && method == "DELETE")) {
			return denied("developers work on existing websites; ask the account owner to add or delete one")
		}
	}
	switch p.Areas[area] {
	case "write":
		return nil
	case "read":
		if method == "GET" {
			return nil
		}
		return denied("you can only look at %s here; ask the account owner for more access", area)
	}
	if area == "billing" || area == "server" {
		return denied("developers can't reach %s", area)
	}
	return denied("you have no access to %s; ask the account owner", area)
}
