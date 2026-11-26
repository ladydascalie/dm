# esctest2 is GPL-2.0. It is fetched, not vendored. The directory is
# gitignored. The gate test is opt-in because the full run takes minutes.

ESCTEST_DIR := internal/esctest/testdata/esctest
ESCTEST_REPO := https://github.com/ThomasDickey/esctest2
ESCTEST_REF := 2798f12149a19c3295e9b4853ab2da4b2eff1b2b

GREMLINS_PKGS = $(shell go list ./internal/... | grep -vE "/(testutil|e2e)$$" | sed "s|^dm/internal/||")
GREMLINS_OUT  := gremlins-results
# The manifest names the run the stored results came from: sha, date and every
# package mutated. `make gremlins` writes it last, so a run that dies midway
# leaves none. `make gremlins-check` refuses results whose sha is not HEAD.
GREMLINS_MANIFEST := $(GREMLINS_OUT)/manifest.txt

GO_TEST_PKGS = $(shell go list ./... 2>/dev/null | wc -l)

.PHONY: help
help:
	@if [ -f $(GREMLINS_MANIFEST) ]; then \
		note="fail unless the manifest records HEAD (recorded: $$(sed -n 's/^sha //p' $(GREMLINS_MANIFEST)) at $$(sed -n 's/^date //p' $(GREMLINS_MANIFEST)))"; \
	else \
		note="fail unless the manifest records HEAD (absent: the stored results are the 2026-11-11/12 sweep, 28 of 44 packages, advisory until the next full run)"; \
	fi; \
	printf '%-16s %s\n' \
		test "go test ./...; opt-in gates stay off because:" \
		"" "DM_ESCTEST=1 runs the esctest2 baseline (GPL-2.0, fetched, minutes)" \
		"" "DM_QRSCAN=1 runs TestQRArtScans, which shells out to zbarimg (not a build dependency)" \
		"" "TestPaneDaemonHelper skips unless the pane daemon spawns it as its own child" \
		"" "RACE=1 adds -race to the same run" \
		esctest-fetch "fetch esctest2 into $(ESCTEST_DIR)" \
		esctest-gate "run the esctest2 baseline gate" \
		gremlins "mutation-test every package, fail on survivors; records sha, date and packages in $(GREMLINS_MANIFEST)" \
		gremlins-pkg "mutation-test one package, e.g. PKG=pane; drops the manifest, so the results no longer claim one run" \
		gremlins-check "$$note" \
		docs-internals "regenerate docs/internals/generated/package-graph.md" \
		provision-check "fail unless this checkout's extension and skill are linked into the harness"

.PHONY: test
test:
	@echo "test: go test $(if $(RACE),-race )./... over $(GO_TEST_PKGS) packages"
	@echo "test: off by default: DM_ESCTEST=1 (esctest2 baseline: GPL-2.0, fetched, slow), DM_QRSCAN=1 (QR art scan: shells out to zbarimg, not a build dependency), TestPaneDaemonHelper (skips unless the pane daemon spawns it)"
	go test $(if $(RACE),-race )./...

.PHONY: esctest-fetch
esctest-fetch:
	@if [ -d $(ESCTEST_DIR)/.git ]; then \
		git -C $(ESCTEST_DIR) fetch --depth 1 origin $(ESCTEST_REF) && \
		git -C $(ESCTEST_DIR) checkout --detach FETCH_HEAD; \
	else \
		mkdir -p $(dir $(ESCTEST_DIR)) && \
		git clone $(ESCTEST_REPO) $(ESCTEST_DIR) && \
		git -C $(ESCTEST_DIR) fetch --depth 1 origin $(ESCTEST_REF) && \
		git -C $(ESCTEST_DIR) checkout --detach FETCH_HEAD; \
	fi

.PHONY: esctest-gate
esctest-gate:
	DM_ESCTEST=1 go test ./internal/esctest/ -run TestEsctestBaseline -v

.PHONY: gremlins
gremlins:
	@mkdir -p $(GREMLINS_OUT)
	@rm -f $(GREMLINS_OUT)/*.json $(GREMLINS_MANIFEST) $(GREMLINS_MANIFEST).partial
	@set -e; trap 'rm -f $(GREMLINS_MANIFEST).partial' EXIT; for p in $(GREMLINS_PKGS); do \
		echo "=== $$p"; \
		gremlins unleash ./internal/$$p -o $(GREMLINS_OUT)/$$p.json > $(GREMLINS_OUT)/$$p.log 2>&1; \
		grep -E "Killed|Lived|Timed out|Mutator coverage|Test efficacy" $(GREMLINS_OUT)/$$p.log | tail -4; \
		if grep -qE '"LIVED"|"TIMED OUT"' $(GREMLINS_OUT)/$$p.json; then \
			echo "FAIL: $$p has surviving mutants"; \
			exit 1; \
		fi; \
		echo "package $$p" >> $(GREMLINS_MANIFEST).partial; \
	done; \
	{ printf 'sha %s\ndate %s\n' "$$(git rev-parse HEAD)" "$$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
	  cat $(GREMLINS_MANIFEST).partial; } > $(GREMLINS_MANIFEST); \
	rm -f $(GREMLINS_MANIFEST).partial; \
	echo "gremlins: all packages clean"; \
	echo "gremlins: recorded $$(git rev-parse --short HEAD) in $(GREMLINS_MANIFEST)"

.PHONY: gremlins-pkg
gremlins-pkg:
	@test -n "$(PKG)" || { echo "usage: make gremlins-pkg PKG=pane"; exit 1; }
	@mkdir -p $(GREMLINS_OUT)
	@rm -f $(GREMLINS_MANIFEST)
	@gremlins unleash ./internal/$(PKG) -o $(GREMLINS_OUT)/$(PKG).json

.PHONY: gremlins-check
gremlins-check:
	@if [ ! -f $(GREMLINS_MANIFEST) ]; then \
		echo "gremlins-check: no $(GREMLINS_MANIFEST): no run recorded its sha, so the stored results are advisory"; \
		exit 1; \
	fi; \
	recorded=$$(sed -n 's/^sha //p' $(GREMLINS_MANIFEST)); \
	when=$$(sed -n 's/^date //p' $(GREMLINS_MANIFEST)); \
	head=$$(git rev-parse HEAD); \
	if [ "$$recorded" != "$$head" ]; then \
		echo "gremlins-check: stale: results recorded at $$recorded ($$when), HEAD is $$head"; \
		exit 1; \
	fi; \
	echo "gremlins-check: results recorded at $$recorded ($$when), current HEAD"

# Local, never a gate: the links live in the operator's home, which CI does not
# have. A symlink cannot go stale, so this catches a one-time setup state only.
.PHONY: provision-check
provision-check:
	go run . provision --check

.PHONY: docs-internals
docs-internals:
	go test -count=1 -run TestGenerated ./internal/archdoc -update
