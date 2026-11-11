# esctest2 is GPL-2.0. It is fetched, not vendored. The directory is
# gitignored. The gate test is opt-in because the full run takes minutes.

ESCTEST_DIR := internal/esctest/testdata/esctest
ESCTEST_REPO := https://github.com/ThomasDickey/esctest2
ESCTEST_REF := 2798f12149a19c3295e9b4853ab2da4b2eff1b2b

GREMLINS_PKGS := $(shell go list ./internal/... | grep -vE "/(testutil|e2e)$$" | sed "s|^dm/internal/||")
GREMLINS_OUT  := gremlins-results

.PHONY: help
help:
	@echo "make esctest-fetch  fetch esctest2 into $(ESCTEST_DIR)"
	@echo "make esctest-gate   run the esctest2 baseline gate"
	@echo "make gremlins        mutation-test every package, fail on survivors"
	@echo "make gremlins-pkg PKG=x   mutation-test one package, e.g. PKG=pane"

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
	@rm -f $(GREMLINS_OUT)/*.json
	@set -e; for p in $(GREMLINS_PKGS); do \
		echo "=== $$p"; \
		gremlins unleash ./internal/$$p -o $(GREMLINS_OUT)/$$p.json > $(GREMLINS_OUT)/$$p.log 2>&1; \
		grep -E "Killed|Lived|Timed out|Mutator coverage|Test efficacy" $(GREMLINS_OUT)/$$p.log | tail -4; \
		if grep -qE '"LIVED"|"TIMED OUT"' $(GREMLINS_OUT)/$$p.json; then \
			echo "FAIL: $$p has surviving mutants"; \
			exit 1; \
		fi; \
		done; \
		echo "gremlins: all packages clean"

.PHONY: gremlins-pkg
gremlins-pkg:
	@test -n "$(PKG)" || { echo "usage: make gremlins-pkg PKG=pane"; exit 1; }
	@mkdir -p $(GREMLINS_OUT)
	@gremlins unleash ./internal/$(PKG) -o $(GREMLINS_OUT)/$(PKG).json
