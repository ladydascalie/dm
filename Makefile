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

# The release matrix. `make dist` builds and tars every row; the workflow in
# .github/workflows/release.yaml runs the same four. VERSION stamps the
# version, and "git describe --tags --always" stands in when it is unset.
DIST_PAIRS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
VERSION ?=
DIST_COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || printf none)

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
		dist "build and tar $(DIST_PAIRS) into out/, then write out/checksums.txt" \
		release-check "go test the release packages, then make dist" \
		provision-check "fail unless this checkout's extension and skill are linked into the harness" \
		docs-site "stage the published markdown and build the GitHub Pages site into site/" \
		docs-casts "record the demo casts the docs site plays, from a scratch state" \
		docs-casts-check "re-check the recorded casts: header, events, input, last frame, webfont" \
		docs-font "rebuild docs/assets/font/dm-cast-mono.woff2 from its upstream" \

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

# dist builds the whole matrix locally, so a lane and the operator can prove
# what the workflow does without GitHub. The tarballs hold one file, dm, at the
# archive root, and checksums.txt is sha256sum format: "cd out && sha256sum -c
# checksums.txt" proves the four.
.PHONY: dist
dist:
	@set -e; \
	trap 'rm -f out/dm' EXIT; \
	rm -f out/dm out/checksums.txt out/dm_*.tar.gz; \
	ver='$(VERSION)'; \
	if [ -z "$$ver" ]; then ver=$$(git describe --tags --always 2>/dev/null || true); fi; \
	ver=$$(printf '%s' "$$ver" | sed 's/^v//'); \
	if [ -z "$$ver" ]; then ver=dev; fi; \
	echo "dist: version $$ver commit $(DIST_COMMIT)"; \
	mkdir -p out; \
	for pair in $(DIST_PAIRS); do \
		goos=$${pair%%/*}; goarch=$${pair##*/}; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -trimpath \
			-ldflags "-s -w -X dm/internal/version.Version=$$ver -X dm/internal/version.Commit=$(DIST_COMMIT)" \
			-o out/dm . ; \
		tar -czf out/dm_$${goos}_$${goarch}.tar.gz -C out dm; \
		echo "dist: dm_$${goos}_$${goarch}.tar.gz"; \
	done; \
	rm -f out/dm; \
	( cd out && sha256sum dm_*.tar.gz > checksums.txt ); \
	ls -l out/dm_*.tar.gz out/checksums.txt

.PHONY: release-check
release-check:
	go test ./internal/version/ ./internal/release/
	$(MAKE) dist

# Local, never a gate: the links live in the operator's home, which CI does not
# have. A symlink cannot go stale, so this catches a one-time setup state only.
.PHONY: provision-check
provision-check:
	go run . provision --check

.PHONY: docs-internals
docs-internals:
	go test -count=1 -run TestGenerated ./internal/archdoc -update

# The Pages site. Never a gate: it needs zensical from requirements-docs.txt,
# which the Go build does not. The workflow runs the same script, so a local
# build and the deployed site cannot disagree.
.PHONY: docs-site
docs-site:
	uv run --quiet --with-requirements requirements-docs.txt ./scripts/docs-site.sh

# A cast is recorded from a live session, by a feeder script and an asciinema
# pty. The taping is local: it needs a terminal, real timing and a whole dm
# server, none of which belong in CI. The recipe is in the tree, so the casts
# are checked in beside the pages that play them. One demo is one file in
# scripts/casts/scenario, and `record.sh --dry-run` prints what each one would
# record. Pass a name to tape one: `make docs-casts CASTS=split`.
.PHONY: docs-casts
docs-casts:
	./scripts/casts/record.sh $(CASTS)

# check.sh needs no recorder and no terminal: it reads the checked-in casts.
.PHONY: docs-casts-check
docs-casts-check:
	./scripts/casts/check.sh docs/casts/*.cast

# The subset carries the glyphs dm draws, and the recorder fails when a cast
# grows one the font lacks. Rebuild it after such a failure, and after a dm
# change that adds a glyph to the rail or the status bar.
.PHONY: docs-font
docs-font:
	./scripts/casts/font.sh
