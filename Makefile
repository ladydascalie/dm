# esctest2 is GPL-2.0. It is fetched, not vendored. The directory is
# gitignored. The gate test is opt-in because the full run takes minutes.

ESCTEST_DIR := internal/esctest/testdata/esctest
ESCTEST_REPO := https://github.com/ThomasDickey/esctest2
ESCTEST_REF := 2798f12149a19c3295e9b4853ab2da4b2eff1b2b

.PHONY: help
help:
	@echo "make esctest-fetch  fetch esctest2 into $(ESCTEST_DIR)"
	@echo "make esctest-gate   run the esctest2 baseline gate"

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
