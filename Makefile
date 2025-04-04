# Makefile to compile main.go for aarch64 (mac), windows, and linux

APP_NAME := pmw
SRC_FILE := main.go
BIN_DIR := bin

build: mac windows linux

mac:
	mkdir -p $(BIN_DIR)
	GOOS=darwin GOARCH=arm64 go build -o $(BIN_DIR)/$(APP_NAME)-mac $(SRC_FILE)

windows:
	mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/$(APP_NAME)-windows.exe $(SRC_FILE)

linux:
	mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/$(APP_NAME)-linux $(SRC_FILE)

clean:
	rm -rf $(BIN_DIR)

.PHONY: build mac windows linux clean