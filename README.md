# Histo

A terminal-based log processing and data visualization tool that displays real-time histograms of tabular data.

## Features

- Processes tabular data from stdin in real-time
- Creates histograms of numeric values
- Supports faceting (grouping) by columns
- Interactive navigation between facets
- Pinning feature to filter data by specific facet values
- Displays statistics (mean, standard deviation, count)
- Colorized visualization with dynamic scaling
- Smooth scrolling and window resizing

## Usage

Example: real-time log analysis of fly.io logs.
```bash
flyctl logs --json \
  | jq '.message |= fromjson' -r \
  | jq 'select(.message.payload.duration_ms != null) | [.message.payload.duration_ms, .message.payload.url, .region, .instance, .message.payload.user] | @tsv' -r \
  | histo
```

### Navigation

- `a/d`: Change facet column
- `←→↑↓`: Navigate between facets
- `Enter`: Pin/unpin a facet (filters data to only show entries matching that facet)
- `0`: Show all facets
- `j/k`: Scroll content
- `q/Ctrl+C`: Quit

## Input Format

Histo expects tab-separated values (TSV) where:
- First column contains the numeric values to plot
- Subsequent columns define facets

Example:
```
10.5    red    seattle
15.2    red    san jose
8.7     blue    seattle
12.1    blue    san jose
```

## Building

```bash
go build
```

## Dependencies

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) - Style definitions for terminal applications


## License

MIT - see [LICENSE](LICENSE) file for details.
