BINARY     := telamon
CONFIG     := config.toml
INSTALL    := /usr/local/bin/$(BINARY)
SERVICE    := telamon.service
SYSTEMD    := /etc/systemd/system/$(SERVICE)
ETC        := /etc/telamon

.PHONY: all deps build run install uninstall \
        service-install service-remove clean

all: build

## Download Go module dependencies
deps:
	GOPROXY=direct GONOSUMDB='*' go mod tidy

## Build the binary
build:
	go build -o $(BINARY) .

## Build and run locally using config.toml
run: build
	./$(BINARY) --config $(CONFIG)

## Install binary to /usr/local/bin
install: build
	install -Dm755 $(BINARY) $(INSTALL)

## Remove installed binary
uninstall:
	rm -f $(INSTALL)

## Install and enable the systemd service
## Copies config + scripts to /etc/telamon if not already present
service-install: install
	@echo "Installing systemd service..."
	install -Dm644 $(SERVICE) $(SYSTEMD)
	@if [ ! -f $(ETC)/$(CONFIG) ]; then \
		mkdir -p $(ETC)/scripts $(ETC)/static; \
		cp $(CONFIG) $(ETC)/$(CONFIG); \
		cp -rn scripts/. $(ETC)/scripts/; \
		echo "Config and scripts copied to $(ETC)/"; \
	else \
		echo "$(ETC)/$(CONFIG) already exists — skipping copy."; \
	fi
	systemctl daemon-reload
	@echo "Done. Run: systemctl enable --now telamon"

## Stop, disable, and remove the systemd service
service-remove:
	@echo "Removing systemd service..."
	-systemctl stop    $(BINARY)
	-systemctl disable $(BINARY)
	rm -f $(SYSTEMD)
	systemctl daemon-reload
	@echo "Service removed."

## Remove the built binary
clean:
	rm -f $(BINARY)
