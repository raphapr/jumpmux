.PHONY: release

release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=x.y.z" >&2; exit 1; }
	@printf '%s\n' "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "VERSION must use x.y.z without the v prefix" >&2; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "working tree must be clean" >&2; exit 1; }
	@! git rev-parse -q --verify "refs/tags/v$(VERSION)" >/dev/null || { echo "tag v$(VERSION) already exists" >&2; exit 1; }
	@go test ./...
	@git tag -a "v$(VERSION)" -m "v$(VERSION)"
	@echo "created v$(VERSION); publish with: git push origin v$(VERSION)"
