# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Rules
- Everything goes in main.go
- Always read the whole file
- Always write the whole file
- Never search within the file, read the whole file
- Never update a portion of the file, write the whole file
- Run go build after writing to main.go
- Only run gofmt before a git commit

## Build Command
- `go build` - Build the application

## Code Style Guidelines
- Follow Go standard formatting with gofmt
- Use clear, descriptive variable and function names
- Group imports: standard lib first, then third-party
- Use section comments to organize code (see main.go)
- Error handling: check errors immediately, provide context
- Prefer early returns for error conditions
- Keep functions focused and reasonably sized
- Include descriptive comments for public API and complex logic
