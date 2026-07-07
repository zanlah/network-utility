# network-utility — build & install the cross-platform tray tools.
#
#   make build       build both tools for this OS into ./bin
#   make windows     cross-build both as .exe into ./bin
#   make install     (macOS) build → ~/Applications/network-utility + auto-start at login
#   make uninstall   (macOS) stop + remove the LaunchAgents and binaries
#   make run-ports / run-netscan   run one tool in the foreground (for testing)
#   make clean       remove ./bin

APPS         := systray-ports systray-netscan
PREFIX       ?= $(HOME)/Applications/network-utility
LAUNCHD_DIR  := $(HOME)/Library/LaunchAgents
LABEL_PREFIX := si.viptronik
BIN          := $(CURDIR)/bin

.PHONY: help build windows install uninstall clean run-ports run-netscan

help:
	@echo "network-utility — targets:"
	@echo "  make build       build both tools into ./bin (this OS)"
	@echo "  make windows     cross-build both as .exe into ./bin"
	@echo "  make install     macOS: install to $(PREFIX) + start at login"
	@echo "  make uninstall   macOS: remove LaunchAgents + binaries"
	@echo "  make run-ports | run-netscan   run one in the foreground"
	@echo "  make clean       remove ./bin"

build:
	@mkdir -p "$(BIN)"
	@for app in $(APPS); do \
		echo "building $$app…"; \
		( cd utilities/$$app && go build -o "$(BIN)/$$app" . ) || exit 1; \
	done
	@echo "→ binaries in ./bin"

windows:
	@mkdir -p "$(BIN)"
	@for app in $(APPS); do \
		echo "building $$app.exe…"; \
		( cd utilities/$$app && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o "$(BIN)/$$app.exe" . ) || exit 1; \
	done
	@echo "→ .exe binaries in ./bin (copy them to Windows; add shortcuts to shell:startup)"

install:
	@[ "$$(uname)" = "Darwin" ] || { echo "make install is macOS-only (uses launchd). Use 'make build' elsewhere."; exit 1; }
	@command -v go >/dev/null || { echo "Go is not installed (https://go.dev/dl)"; exit 1; }
	@mkdir -p "$(PREFIX)" "$(LAUNCHD_DIR)"
	@for app in $(APPS); do \
		echo "building + installing $$app…"; \
		( cd utilities/$$app && go build -o "$(PREFIX)/$$app" . ) || exit 1; \
		label="$(LABEL_PREFIX).$$app"; \
		plist="$(LAUNCHD_DIR)/$$label.plist"; \
		sed -e "s|@LABEL@|$$label|g" -e "s|@BINARY@|$(PREFIX)/$$app|g" packaging/launchagent.plist.in > "$$plist"; \
		launchctl unload "$$plist" 2>/dev/null || true; \
		launchctl load "$$plist" || { echo "launchctl load failed for $$app"; exit 1; }; \
		echo "  → $(PREFIX)/$$app (running + starts at login)"; \
	done
	@echo "Installed. 🔌 and 📡 should appear in the menu bar."
	@echo "(netscan asks for Local Network permission on its first scan — allow it.)"

uninstall:
	@[ "$$(uname)" = "Darwin" ] || { echo "make uninstall is macOS-only."; exit 1; }
	@for app in $(APPS); do \
		label="$(LABEL_PREFIX).$$app"; \
		plist="$(LAUNCHD_DIR)/$$label.plist"; \
		launchctl unload "$$plist" 2>/dev/null || true; \
		rm -f "$$plist" "$(PREFIX)/$$app"; \
		echo "removed $$app"; \
	done
	@echo "Uninstalled. (Left settings in place; 'rm -rf $(PREFIX)' to purge them too.)"

run-ports:
	cd utilities/systray-ports && go run .

run-netscan:
	cd utilities/systray-netscan && go run .

clean:
	rm -rf "$(BIN)"
