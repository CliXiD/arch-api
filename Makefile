.PHONY: openapi openapi-check sqlc

# Regenerate the API contract from the Huma types. Commit the result.
openapi:
	go run ./cmd/openapi > api/openapi.yaml

# Fail when api/openapi.yaml is stale. Writes only a temp file, never a tracked one.
openapi-check:
	@tmp=$$(mktemp); \
	go run ./cmd/openapi > "$$tmp" || { rm -f "$$tmp"; exit 1; }; \
	diff -u api/openapi.yaml "$$tmp"; status=$$?; \
	rm -f "$$tmp"; \
	if [ $$status -ne 0 ]; then echo "api/openapi.yaml is stale: run make openapi" >&2; fi; \
	exit $$status

# Regenerate the database code in internal/db/dbq from migrations/ and internal/db/queries/.
# Commit the result; never edit it by hand.
sqlc:
	go tool sqlc generate
