package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// -------------------------
// Model and Global Styles
// -------------------------

// model holds the application state.
type model struct {
	// facetsData maps facet column (1-indexed) to a map of facet value → slice of numbers.
	facetsData map[int]map[string][]float64

	// storedLines stores all input lines for reprocessing when pins change
	storedLines []string

	totalLogCount int
	startTime     time.Time

	// facet: if nonzero, display only that facet column; 0 means show all facets.
	facet int
	// stats: if true, show summary stats (mean, stdev, count) instead of a full histogram.
	stats bool
	// lines receives raw lines from STDIN.
	lines chan string

	// Window dimensions.
	winWidth, winHeight int
	// scrollOffset tracks how far the content has been scrolled.
	scrollOffset int

	// For non-float values in the first column
	stringValues map[string]int
	// countStrings: if true, count occurrences of non-float strings in the first column
	countStrings bool

	// Navigation
	activeFacet     string
	activeFacetPos  [2]int // [row, col]
	activeFacetKeys []string
	facetPositions  map[string][2]int

	// Grid dimensions for consistent navigation
	gridColumns int
	gridRows    int

	// Pinning feature
	pinnedFacets       map[string]bool              // key: facet value, value: true if pinned
	pinnedFacetsColumn map[string]int               // key: facet value, value: column index (1-indexed)
	filteredData       map[int]map[string][]float64 // filtered data based on pins
	isFiltered         bool                         // true if at least one facet is pinned
}

// tickMsg is used for periodic updates.
type tickMsg struct{}

// panelStyle is a Lip Gloss style for panels.
var panelStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("250")). // Light gray border
	Padding(1, 2).
	Margin(1)

// activePanelStyle is used for the currently selected facet
// Using extremely distinct styling to make the active panel obvious
var activePanelStyle = lipgloss.NewStyle().
	Border(lipgloss.ThickBorder()).         // Much thicker border
	BorderForeground(lipgloss.Color("39")). // Cyan for active panel
	BorderBackground(lipgloss.Color("23")). // Dark blue background for border
	Foreground(lipgloss.Color("15")).       // White text
	Bold(true).
	Padding(1, 2).
	Margin(1)

// pinnedPanelStyle is used for pinned facets
var pinnedPanelStyle = lipgloss.NewStyle().
	Border(lipgloss.DoubleBorder()).         // Double border for pinned facets
	BorderForeground(lipgloss.Color("205")). // Pink/magenta for pinned facets
	Foreground(lipgloss.Color("15")).        // White text
	Bold(true).
	Padding(1, 2).
	Margin(1)

// activePinnedPanelStyle is used for facets that are both active and pinned
var activePinnedPanelStyle = lipgloss.NewStyle().
	Border(lipgloss.ThickBorder()).          // Thick border from active
	BorderForeground(lipgloss.Color("205")). // Pink/magenta from pinned
	BorderBackground(lipgloss.Color("23")).  // Dark blue background from active
	Foreground(lipgloss.Color("15")).        // White text
	Bold(true).
	Padding(1, 2).
	Margin(1)

// -------------------------
// Commands and Init
// -------------------------

// tickCmd returns a command that sends a tickMsg every 500ms.
func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

// Init starts the background goroutine that reads STDIN.
func (m *model) Init() tea.Cmd {
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			m.lines <- scanner.Text()
		}
		close(m.lines)
	}()
	return tickCmd()
}

// -------------------------
// Update
// -------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tickMsg:
		// Drain any available lines (nonblocking)
		for {
			select {
			case line, ok := <-m.lines:
				if !ok {
					break
				}
				m.processLine(line)
			default:
				goto done
			}
		}
	done:
		return m, tickCmd()

	case tea.WindowSizeMsg:
		m.winWidth = msg.Width
		m.winHeight = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		// Quit the program.
		case "ctrl+c", "q":
			return m, tea.Quit

		// Switch facets with "a" and "d" keys
		case "a":
			if m.facet > 0 {
				m.facet--
				// Reset scroll when switching facets.
				m.scrollOffset = 0
				// Reset active facet
				m.resetActiveFacet()
			}
			return m, nil

		case "d":
			// Determine maximum facet available.
			dataSource := m.facetsData
			if m.isFiltered {
				dataSource = m.filteredData
			}

			maxFacet := 0
			for k := range dataSource {
				if k > maxFacet {
					maxFacet = k
				}
			}
			if m.facet == 0 && maxFacet > 0 {
				m.facet = 1
				m.scrollOffset = 0
				m.resetActiveFacet()
			} else if m.facet < maxFacet {
				m.facet++
				m.scrollOffset = 0
				m.resetActiveFacet()
			}
			return m, nil

		// Navigate between histograms with arrow keys
		case "left":
			m.navigateGrid(-1, 0)
			return m, nil

		case "right":
			m.navigateGrid(1, 0)
			return m, nil

		// Reset view to show all facets.
		case "0":
			m.facet = 0
			m.scrollOffset = 0
			m.resetActiveFacet()
			return m, nil

		// Scroll content with j/k
		case "k":
			m.scrollOffset--
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}
			return m, nil

		case "j":
			m.scrollOffset++
			return m, nil

		// Navigate between histograms with arrow keys
		case "up":
			m.navigateGrid(0, -1)
			return m, nil

		case "down":
			m.navigateGrid(0, 1)
			return m, nil

		// Implement pinning with Enter key
		case "enter":
			// Only pin if we have an active facet
			if m.activeFacet != "" {
				// Toggle pin state
				if m.pinnedFacets[m.activeFacet] {
					// Unpin this facet
					delete(m.pinnedFacets, m.activeFacet)
					delete(m.pinnedFacetsColumn, m.activeFacet)
				} else {
					// Pin this facet
					m.pinnedFacets[m.activeFacet] = true

					// Store which column this facet belongs to
					if m.facet > 0 {
						// If we're in a single facet view, use that facet number
						m.pinnedFacetsColumn[m.activeFacet] = m.facet
					} else {
						// In the all-facets view, determine the column from our position
						// For each facet column
						for facetCol, facetMap := range m.facetsData {
							if _, exists := facetMap[m.activeFacet]; exists {
								m.pinnedFacetsColumn[m.activeFacet] = facetCol
								break
							}
						}
					}
				}

				// Check if we need to update filtered status
				m.isFiltered = len(m.pinnedFacets) > 0

				// If we have pins, regenerate filtered data
				if m.isFiltered {
					m.regenerateFilteredData()
				}
			}
			return m, nil

		default:
			return m, nil
		}

	default:
		return m, nil
	}
}

// regenerateFilteredData recreates the filtered dataset based on pinned facets
func (m *model) regenerateFilteredData() {
	// Reset the filtered data structure
	m.filteredData = make(map[int]map[string][]float64)

	// Initialize each facet column in filtered data
	for facetCol := range m.facetsData {
		m.filteredData[facetCol] = make(map[string][]float64)
	}

	// Reprocess all stored lines with the current pin configuration
	for _, line := range m.storedLines {
		m.processLineWithFilter(line, true)
	}
}

// navigateGrid handles all grid navigation in a consistent manner
func (m *model) navigateGrid(dx, dy int) {
	dataSource := m.facetsData
	if m.isFiltered {
		dataSource = m.filteredData
	}

	if m.facet == 0 {
		// In all-facets view
		if m.activeFacet == "" {
			// Initialize active facet if not set
			for facetCol := range dataSource {
				// Get sorted keys to initialize with the first displayed facet
				facetData := dataSource[facetCol]
				keys := getSortedFacetKeys(facetData)
				if len(keys) > 0 {
					m.activeFacet = keys[0]
					break
				}
			}
			if m.activeFacet == "" {
				return // No facets available
			}
		}

		// For multi-facet view, handle direct navigation through facet keys
		// This is simpler than trying to navigate by row/column coordinates
		allKeys := []string{}
		for facetCol := range dataSource {
			facetData := dataSource[facetCol]
			keys := getSortedFacetKeys(facetData)
			allKeys = append(allKeys, keys...)
		}

		if len(allKeys) == 0 {
			return // No keys available
		}

		// Find current index of activeFacet in allKeys
		currentIndex := -1
		for i, key := range allKeys {
			if key == m.activeFacet {
				currentIndex = i
				break
			}
		}

		if currentIndex == -1 {
			// Active facet not found, select first key
			m.activeFacet = allKeys[0]
			m.updatePositionFromActiveFacet()
			return
		}

		// Calculate new index with bounds checking
		var newIndex int
		if dy > 0 {
			// Moving down
			newIndex = currentIndex + 1
			if newIndex >= len(allKeys) {
				newIndex = len(allKeys) - 1 // Stay at last item
			}
		} else if dy < 0 {
			// Moving up
			newIndex = currentIndex - 1
			if newIndex < 0 {
				newIndex = 0 // Stay at first item
			}
		} else if dx != 0 {
			// Left/right movement - do nothing in multi-facet view
			// since it's a vertical list
			return
		} else {
			return // No movement
		}

		// Update active facet
		m.activeFacet = allKeys[newIndex]
		m.updatePositionFromActiveFacet()
		m.ensureActiveFacetVisible()

	} else if m.facet > 0 {
		// Single facet view
		facetData, ok := dataSource[m.facet]
		if !ok {
			return
		}

		keys := getSortedFacetKeys(facetData)
		if len(keys) == 0 {
			return
		}

		// Find current index
		currentIndex := -1
		for i, key := range keys {
			if key == m.activeFacet {
				currentIndex = i
				break
			}
		}

		if currentIndex == -1 {
			// Not found, select first
			m.activeFacet = keys[0]
			m.activeFacetPos = [2]int{0, 0}
			return
		}

		// Calculate grid dimensions
		columns := m.gridColumns
		if columns < 1 {
			columns = max(1, m.winWidth/60) // Use a reasonable estimate if not set
		}

		// Calculate current row and column
		currentRow := currentIndex / columns
		currentCol := currentIndex % columns

		// Calculate target position
		targetRow := currentRow + dy
		targetCol := currentCol + dx

		// Check boundaries
		if targetRow < 0 || targetCol < 0 {
			return
		}

		// Calculate target index
		targetIndex := targetRow*columns + targetCol

		// Check if valid
		if targetIndex >= 0 && targetIndex < len(keys) {
			m.activeFacet = keys[targetIndex]
			m.activeFacetPos = [2]int{targetRow, targetCol}
			m.ensureActiveFacetVisible()
		}
	}
}

// abs returns the absolute value of an integer
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// resetActiveFacet initializes the active facet state when switching views
func (m *model) resetActiveFacet() {
	dataSource := m.facetsData
	if m.isFiltered {
		dataSource = m.filteredData
	}

	if m.facet == 0 {
		// In all facets view
		// Reset active facet - will be set by the first render
		m.activeFacet = ""
		m.activeFacetPos = [2]int{0, 0}

		// Initialize with the first key from the sorted facets
		for facetCol := range dataSource {
			facetData := dataSource[facetCol]
			keys := getSortedFacetKeys(facetData)
			if len(keys) > 0 {
				m.activeFacet = keys[0]
				break
			}
		}
	} else {
		// In single facet view, reset position and set active facet if not already set
		m.activeFacetPos = [2]int{0, 0}

		// Initialize activeFacet to the first item in the current facet if it's empty
		if facetData, ok := dataSource[m.facet]; ok {
			keys := getSortedFacetKeys(facetData)
			if len(keys) > 0 {
				m.activeFacet = keys[0]
			}
		}
	}
}

// updatePositionFromActiveFacet updates the position based on the current active facet
func (m *model) updatePositionFromActiveFacet() {
	if pos, exists := m.facetPositions[m.activeFacet]; exists {
		m.activeFacetPos = pos
	}
}

// getSortedFacetKeys returns the keys from a facet map sorted by mean value
func getSortedFacetKeys(facetData map[string][]float64) []string {
	// Build a slice of keys and sort them by descending mean
	keys := make([]string, 0, len(facetData))
	for k := range facetData {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		meanI := computeMean(facetData[keys[i]])
		meanJ := computeMean(facetData[keys[j]])
		if meanI != meanJ {
			return meanI > meanJ
		}
		return keys[i] < keys[j] // secondary sort by key name for stability
	})
	return keys
}

// ensureActiveFacetVisible ensures the active facet is visible by adjusting scroll
func (m *model) ensureActiveFacetVisible() {
	if m.activeFacet == "" {
		return
	}

	// If position isn't known yet, skip
	if _, exists := m.facetPositions[m.activeFacet]; !exists {
		return
	}

	// Determine row position
	row := m.activeFacetPos[0]

	// Calculate row height - simple estimate based on visual panel size
	rowHeight := 15

	// Calculate content area height
	staticHeight := 10 // Estimate for header and instructions
	availableHeight := m.winHeight - staticHeight

	// Calculate row boundaries
	startRow := m.scrollOffset / rowHeight
	endRow := (m.scrollOffset + availableHeight) / rowHeight

	// Adjust scroll if active facet is not visible
	if row < startRow {
		// Scroll up to make the active facet visible
		m.scrollOffset = row * rowHeight
	} else if row >= endRow {
		// Scroll down to make the active facet visible
		m.scrollOffset = (row-endRow+1)*rowHeight + 1
	}
}

// processLine handles a single line of input, storing it for reprocessing if needed
func (m *model) processLine(line string) {
	// Store the line for potential reprocessing when pins change
	m.storedLines = append(m.storedLines, line)

	// Process the line normally for the main data structure
	m.processLineWithFilter(line, false)

	// If we have active filters, also process for filtered data
	if m.isFiltered {
		m.processLineWithFilter(line, true)
	}
}

// processLineWithFilter processes a line with optional filtering based on pins
func (m *model) processLineWithFilter(line string, applyFilter bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	parts := strings.Split(line, "\t")
	if len(parts) < 1 {
		return
	}

	// Try to parse as float64
	value, err := strconv.ParseFloat(parts[0], 64)

	// For filtered data, check if this line should be included based on pins
	if applyFilter && len(m.pinnedFacets) > 0 {
		// Check if all pinned facets match
		for pinnedValue, isActive := range m.pinnedFacets {
			if !isActive {
				continue // Skip non-active pins
			}

			pinnedCol := m.pinnedFacetsColumn[pinnedValue]
			// pinnedCol is 1-indexed, parts array is 0-indexed
			if pinnedCol < len(parts) {
				facetValue := parts[pinnedCol]
				if facetValue != pinnedValue {
					// This line doesn't match a pin, skip it
					return
				}
			}
		}
	}

	// Handle non-float values (always count strings)
	if err != nil {
		if !applyFilter {
			// Only update global counters when not in filter mode
			m.stringValues[parts[0]]++
			m.totalLogCount++
		}
		return
	}

	// Only increment log count once per line (not for filtered processing)
	if !applyFilter {
		m.totalLogCount++
	}

	// Determine which data structure to update
	targetData := m.facetsData
	if applyFilter {
		targetData = m.filteredData
	}

	// For each subsequent column, update the appropriate data structure
	for i, facet := range parts[1:] {
		index := i + 1 // facets are 1-indexed
		if targetData[index] == nil {
			targetData[index] = make(map[string][]float64)
		}
		targetData[index][facet] = append(targetData[index][facet], value)
	}
}

// -------------------------
// Helper Functions
// -------------------------

// computeMean returns the mean of a slice of float64.
func computeMean(values []float64) float64 {
	if len(values) == 0 {
		return 0.0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// globalRange computes the overall min and max across all facets.
func (m model) globalRange() (gmin, gmax float64, ok bool) {
	dataSource := m.facetsData
	if m.isFiltered {
		dataSource = m.filteredData
	}

	allValues := []float64{}
	for _, facetMap := range dataSource {
		for _, values := range facetMap {
			allValues = append(allValues, values...)
		}
	}
	if len(allValues) == 0 {
		return 0, 0, false
	}
	gmin = allValues[0]
	gmax = allValues[0]
	for _, v := range allValues {
		if v < gmin {
			gmin = v
		}
		if v > gmax {
			gmax = v
		}
	}
	return gmin, gmax, true
}

// renderStringHistogram creates a horizontal bar chart for string values.
func (m model) renderStringHistogram() string {
	if len(m.stringValues) == 0 {
		return "No string values found."
	}

	// Sort strings by count (descending)
	type stringCount struct {
		value string
		count int
	}
	counts := make([]stringCount, 0, len(m.stringValues))
	for value, count := range m.stringValues {
		counts = append(counts, stringCount{value, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count != counts[j].count {
			return counts[i].count > counts[j].count
		}
		return counts[i].value < counts[j].value // secondary sort by value for stability
	})

	// Find the maximum count for scaling
	maxCount := counts[0].count

	// Create the histogram
	var builder strings.Builder
	barWidth := m.winWidth / 2

	for _, item := range counts {
		// Scale the bar length
		barLength := int(float64(item.count) / float64(maxCount) * float64(barWidth))
		if barLength < 3 {
			barLength = 3
		}

		bar := strings.Repeat("█", barLength)
		line := fmt.Sprintf("%-20s %5d %s\n", item.value, item.count, bar)
		builder.WriteString(line)
	}

	return builder.String()
}

// createVerticalHistogram builds a vertical bar histogram as a multiline string.
// It divides the global range [gmin, gmax] into binCount bins and scales the height to barHeight.
func createVerticalHistogram(values []float64, gmin, gmax float64, binCount, barHeight int) string {
	if len(values) == 0 {
		return "No data"
	}
	if gmin == gmax {
		bar := ""
		for i := 0; i < barHeight; i++ {
			bar += "█ "
		}
		return bar + fmt.Sprintf("\n%.2f", gmin)
	}
	binSize := (gmax - gmin) / float64(binCount)
	bins := make([]int, binCount)
	for _, v := range values {
		idx := int((v - gmin) / binSize)
		if idx >= binCount {
			idx = binCount - 1
		}
		bins[idx]++
	}
	maxCount := 0
	for _, count := range bins {
		if count > maxCount {
			maxCount = count
		}
	}
	normalized := make([]int, binCount)
	for i, count := range bins {
		if count > 0 {
			// Ensure at least a height of 1 for any non-zero count
			normalized[i] = max(1, int((float64(count)/float64(maxCount))*float64(barHeight)))
		} else {
			normalized[i] = 0
		}
	}
	var rows []string
	for row := barHeight; row > 0; row-- {
		var rowStr string
		for _, h := range normalized {
			if h >= row {
				rowStr += "█ "
			} else {
				rowStr += "  "
			}
		}
		rows = append(rows, rowStr)
	}
	// Build bottom label row showing the midpoints.
	var labelParts []string
	for i := 0; i < binCount; i++ {
		midpoint := gmin + (float64(i)+0.5)*binSize
		labelParts = append(labelParts, fmt.Sprintf("%4.1f", midpoint))
	}
	labelRow := strings.Join(labelParts, " ")
	return strings.Join(rows, "\n") + "\n" + labelRow
}

// -------------------------
// Rendering Functions
// -------------------------

// renderHeader creates a header string showing log rate, total count, and pin status.
func (m model) renderHeader() string {
	elapsed := time.Since(m.startTime).Seconds()
	rate := 0.0
	if elapsed > 0 {
		rate = float64(m.totalLogCount) / elapsed
	}

	header := fmt.Sprintf("Log Rate: %.2f logs/sec | Total Logs: %d", rate, m.totalLogCount)

	// Add information about pinned facets if any
	if m.isFiltered && len(m.pinnedFacets) > 0 {
		pinnedInfo := " | Pins: "
		first := true
		for facet, isPinned := range m.pinnedFacets {
			if isPinned {
				if !first {
					pinnedInfo += ", "
				}
				col := m.pinnedFacetsColumn[facet]
				pinnedInfo += fmt.Sprintf("%d:%s", col, facet)
				first = false
			}
		}
		header += pinnedInfo
	}

	// Add active facet info for debugging
	if m.activeFacet != "" {
		header += fmt.Sprintf(" | Active: %s", m.activeFacet)
	}

	return lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(true).
		Render(header)
}

// wrapText wraps text to a specified width, preserving words when possible
func wrapText(text string, width int, maxHeight int) string {
	if width <= 0 || len(text) <= width {
		return text
	}

	var wrapped strings.Builder
	words := strings.Fields(text)

	lineCount := 0
	line := ""
	for _, word := range words {
		if len(line)+len(word)+1 <= width {
			if line != "" {
				line += " "
			}
			line += word
		} else {
			if line != "" {
				wrapped.WriteString(line + "\n")
				lineCount++

				// Check if we've reached maxHeight
				if maxHeight > 0 && lineCount >= maxHeight {
					return wrapped.String() + "..."
				}
			}
			// If a single word is longer than width, we need to force-break it
			if len(word) > width {
				for len(word) > 0 {
					if len(word) <= width {
						line = word
						break
					}
					wrapped.WriteString(word[:width] + "\n")
					lineCount++

					// Check if we've reached maxHeight
					if maxHeight > 0 && lineCount >= maxHeight {
						return wrapped.String() + "..."
					}

					word = word[width:]
				}
			} else {
				line = word
			}
		}
	}

	if line != "" {
		wrapped.WriteString(line)
	}

	return wrapped.String()
}

// renderSingleFacet builds panels for a single facet column and arranges them in a grid.
func (m model) renderSingleFacet() string {
	dataSource := m.facetsData
	if m.isFiltered {
		dataSource = m.filteredData
	}

	facetData, ok := dataSource[m.facet]
	if !ok {
		return "Facet not available yet."
	}

	// Build a slice of keys and sort them by descending mean.
	keys := getSortedFacetKeys(facetData)

	gmin, gmax, found := m.globalRange()
	if !found {
		return "No data yet."
	}

	// Constants for consistent panel dimensions
	const maxKeyWidth = 64 // Maximum width for facet keys before wrapping
	const maxKeyHeight = 3 // Maximum height for wrapped facet keys
	const plotWidth = 70   // Width limit for the entire plot

	// Check if any titles wrap to two lines by wrapping all titles first
	wrappedTitles := make([]string, len(keys))
	anyWrapped := false
	for i, key := range keys {
		displayKey := key
		if m.pinnedFacets[key] {
			displayKey = fmt.Sprintf("📌 %s", key)
		}
		wrappedTitles[i] = wrapText(displayKey, maxKeyWidth, maxKeyHeight)
		if strings.Contains(wrappedTitles[i], "\n") {
			anyWrapped = true
		}
	}

	// Create panels with consistent heights
	var panels []string
	for i, key := range keys {
		values := facetData[key]
		var content string
		if m.stats {
			mean := computeMean(values)
			var variance float64
			for _, v := range values {
				variance += (v - mean) * (v - mean)
			}
			variance /= float64(len(values))
			stdev := math.Sqrt(variance)
			content = fmt.Sprintf("Mean: %.2f\nStd Dev: %.2f\nCount: %d", mean, stdev, len(values))
		} else {
			content = createVerticalHistogram(values, gmin, gmax, 10, 10)
		}

		// Use different styles based on active and pinned status
		var panel string
		var titleContent string

		// Use the pre-wrapped title
		titleContent = wrappedTitles[i]

		// If any title wrapped to two lines, ensure all titles have at least two lines
		// by adding an extra newline to single-line titles
		if anyWrapped && !strings.Contains(titleContent, "\n") {
			titleContent = titleContent + "\n"
		}

		// Render the panel with wrapped text
		if key == m.activeFacet && m.pinnedFacets[key] {
			// Both active and pinned
			panel = activePinnedPanelStyle.Render(fmt.Sprintf("%s\n\n%s", titleContent, content))
		} else if key == m.activeFacet {
			// Just active
			panel = activePanelStyle.Render(fmt.Sprintf("%s\n\n%s", titleContent, content))
		} else if m.pinnedFacets[key] {
			// Just pinned
			panel = pinnedPanelStyle.Render(fmt.Sprintf("%s\n\n%s", titleContent, content))
		} else {
			// Neither
			panel = panelStyle.Render(fmt.Sprintf("%s\n\n%s", titleContent, content))
		}
		panels = append(panels, panel)
	}

	// Calculate grid layout for navigation
	// Use real panel width to determine columns that fit
	var panelWidth int
	if len(panels) > 0 {
		panelWidth = lipgloss.Width(panels[0])
	} else {
		panelWidth = 60 // Default if no panels
	}

	columns := max(1, m.winWidth/panelWidth)
	m.gridColumns = columns

	// Create return grid
	return renderGridLayout(panels, columns)
}

// renderGridLayout arranges panels in a grid
func renderGridLayout(panels []string, columns int) string {
	if len(panels) == 0 {
		return ""
	}

	// Arrange panels in rows
	var rows []string
	for rowIdx := 0; rowIdx < len(panels); rowIdx += columns {
		end := rowIdx + columns
		if end > len(panels) {
			end = len(panels)
		}

		row := lipgloss.JoinHorizontal(lipgloss.Top, panels[rowIdx:end]...)
		rows = append(rows, row)
	}

	return lipgloss.JoinVertical(lipgloss.Top, rows...)
}

// renderMultiFacet renders a summary for all facet columns.
func (m model) renderMultiFacet() string {
	var output strings.Builder

	dataSource := m.facetsData
	if m.isFiltered {
		dataSource = m.filteredData
	}

	// First determine global min/max for consistent bucketing
	gmin, gmax, found := m.globalRange()
	if !found {
		return "No data yet."
	}

	// Number of buckets for histogram representation
	const bucketCount = 20
	bucketSize := (gmax - gmin) / float64(bucketCount)

	// Sort facet numbers for consistent rendering order in summary stats
	facets := make([]int, 0, len(dataSource))
	for facet := range dataSource {
		facets = append(facets, facet)
	}
	sort.Ints(facets)

	// Reset facet positions for navigation
	m.facetPositions = make(map[string][2]int)

	// Track row position for facet navigation
	rowPosition := 0

	// Reset active facet keys
	m.activeFacetKeys = nil

	// If the active facet isn't set yet, initialize it with the first key from sorted keys
	// This ensures consistent navigation starting point
	firstFacetKey := ""
	for _, facet := range facets {
		facetData := dataSource[facet]
		keys := getSortedFacetKeys(facetData)
		if len(keys) > 0 {
			firstFacetKey = keys[0]
			break
		}
	}

	if m.activeFacet == "" && firstFacetKey != "" {
		m.activeFacet = firstFacetKey
	}

	for _, facet := range facets {
		facetData := dataSource[facet]
		output.WriteString(fmt.Sprintf("Facet %d:\n", facet))

		// Find the max key length across all facets for consistent alignment
		globalMaxKeyLength := 0
		for _, facetMap := range dataSource {
			for key := range facetMap {
				keyLen := len(key)
				// Add extra width for pin emoji if this key is pinned
				if m.pinnedFacets[key] {
					keyLen += 3 // Width of "📌 " (emoji + space)
				}
				if keyLen > globalMaxKeyLength {
					globalMaxKeyLength = keyLen
				}
			}
		}
		// Ensure we have enough space for buckets
		if globalMaxKeyLength < 10 {
			globalMaxKeyLength = 10
		}
		// Use global max length for consistent alignment
		maxKeyLength := globalMaxKeyLength

		// Build a slice of keys and sort them by descending mean
		keys := getSortedFacetKeys(facetData)

		// Add to active facet keys for navigation
		m.activeFacetKeys = append(m.activeFacetKeys, keys...)

		// Calculate max count across all buckets for color normalization
		maxBucketCount := 0
		for _, key := range keys {
			values := facetData[key]
			buckets := make([]int, bucketCount)

			// Distribute values into buckets
			for _, v := range values {
				idx := int((v - gmin) / bucketSize)
				if idx >= bucketCount {
					idx = bucketCount - 1
				} else if idx < 0 {
					idx = 0
				}
				buckets[idx]++
				if buckets[idx] > maxBucketCount {
					maxBucketCount = buckets[idx]
				}
			}
		}

		// Show bucket scale at the top
		output.WriteString("  ")
		output.WriteString(strings.Repeat(" ", maxKeyLength))
		output.WriteString("  ")

		for i := 0; i < bucketCount; i++ {
			if i%5 == 0 {
				val := gmin + float64(i)*bucketSize
				output.WriteString(fmt.Sprintf("%-5.1f", val))
			} else {
				output.WriteString("     ")
			}
		}
		output.WriteString(fmt.Sprintf("%-5.1f\n", gmax))

		// Display colorized histograms for each key
		for _, key := range keys {
			values := facetData[key]
			mean := computeMean(values)
			var variance float64
			for _, v := range values {
				variance += (v - mean) * (v - mean)
			}
			variance /= float64(len(values))
			stdev := math.Sqrt(variance)

			buckets := make([]int, bucketCount)

			// Distribute values into buckets
			for _, v := range values {
				idx := int((v - gmin) / bucketSize)
				if idx >= bucketCount {
					idx = bucketCount - 1
				} else if idx < 0 {
					idx = 0
				}
				buckets[idx]++
			}

			// Format stats
			stats := fmt.Sprintf("μ=%.2f σ=%.2f n=%d", mean, stdev, len(values))

			// Store position for navigation before styling
			// Use flat 2D layout - each key gets its own row in this facet
			m.facetPositions[key] = [2]int{rowPosition, 0}

			// Update active facet position if this is our active facet
			if key == m.activeFacet {
				m.activeFacetPos = [2]int{rowPosition, 0}
			}

			rowPosition++

			// Output the key name with proper padding
			keyStyle := lipgloss.NewStyle()

			// Different styling based on active/pinned status
			var formattedKey string
			if key == m.activeFacet && m.pinnedFacets[key] {
				// Both active and pinned
				keyStyle = keyStyle.Foreground(lipgloss.Color("205")).Bold(true).Background(lipgloss.Color("23"))
				// Emoji 📌 is a multi-byte character but displays as single width
				formattedKey = keyStyle.Render(fmt.Sprintf("📌 %-*s", maxKeyLength-3, key))
			} else if key == m.activeFacet {
				// Just active
				keyStyle = keyStyle.Foreground(lipgloss.Color("15")).Bold(true).Background(lipgloss.Color("27"))
				formattedKey = keyStyle.Render(fmt.Sprintf("%-*s", maxKeyLength, key))
			} else if m.pinnedFacets[key] {
				// Just pinned
				keyStyle = keyStyle.Foreground(lipgloss.Color("205"))
				// Emoji 📌 is a multi-byte character but displays as single width
				formattedKey = keyStyle.Render(fmt.Sprintf("📌 %-*s", maxKeyLength-3, key))
			} else {
				// Neither
				formattedKey = keyStyle.Render(fmt.Sprintf("%-*s", maxKeyLength, key))
			}

			output.WriteString(fmt.Sprintf("  %s", formattedKey))

			// Output histogram with colored squares
			output.WriteString("  ")
			for _, count := range buckets {
				// Calculate color intensity based on logarithmic scale of count
				if count == 0 {
					output.WriteString("·    ") // Empty bucket
				} else {
					// Use logarithmic scale for better dynamic range
					logCount := math.Log1p(float64(count)) // log(1+count) to handle count=1 case
					logMax := math.Log1p(float64(maxBucketCount))

					// Normalize to range 0.0-1.0
					normalized := logCount / logMax

					// Map to a color spectrum from blue (low) to red (high)
					// Using a wider range of terminal colors (16-231)
					// Colors 196-201: red-orange
					// Colors 202-208: orange-yellow
					// Colors 40-46: green
					// Colors 27-33: blue

					var color int
					switch {
					case normalized < 0.25:
						// Blue range (27-33)
						color = 27 + int(normalized*24)
					case normalized < 0.5:
						// Green range (40-46)
						color = 40 + int((normalized-0.25)*24)
					case normalized < 0.75:
						// Yellow range (202-208)
						color = 202 + int((normalized-0.5)*24)
					default:
						// Red range (196-201)
						color = 196 + int((normalized-0.75)*20)
					}

					square := lipgloss.NewStyle().
						Background(lipgloss.Color(fmt.Sprintf("%d", color))).
						Render(" ")

					output.WriteString(square + "    ")
				}
			}

			// Output stats after the histogram
			output.WriteString(" " + stats + "\n")
		}
		output.WriteString("\n")
	}
	return output.String()
}

// renderColorGradient displays the color gradient used in the visualization
func renderColorGradient() string {
	var builder strings.Builder

	// The 4 color ranges used in the application
	colorRanges := []struct {
		start int
		end   int
	}{
		{27, 33},
		{40, 46},
		{202, 208},
		{196, 201},
	}

	// Display each color in the gradient with spacing
	for _, colorRange := range colorRanges {
		for color := colorRange.start; color <= colorRange.end; color++ {
			square := lipgloss.NewStyle().
				Background(lipgloss.Color(fmt.Sprintf("%d", color))).
				Render("  ")
			builder.WriteString(square)
		}
		builder.WriteString(" ")
	}

	return builder.String()
}

// View renders the complete UI, including scrolling the content.
func (m model) View() string {
	header := m.renderHeader()

	// Render instructions
	instructions := lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")).
		Render("a/d: Change Facet | ←→↑↓: Navigate | Enter: Pin | 0: All Facets | j/k: Scroll | q/Ctrl+C: Quit")

	var content string
	if len(m.stringValues) > 0 {
		content = m.renderStringHistogram()
	} else if m.facet != 0 {
		content = m.renderSingleFacet()
	} else {
		content = m.renderMultiFacet()
	}

	// Add the color gradient legend only to the multi-facet view
	if m.facet == 0 && len(m.stringValues) == 0 {
		content += renderColorGradient()
	}

	// Combine header/instructions and content.
	// We'll apply scrolling only to the content portion.
	staticPart := header + "\n\n" + instructions + "\n\n"

	// Split content into lines.
	contentLines := strings.Split(content, "\n")
	// Calculate available height for content.
	staticHeight := lipgloss.Height(staticPart)
	availableHeight := m.winHeight - staticHeight
	if availableHeight < 1 {
		availableHeight = 1
	}
	// Clamp scroll offset.
	maxScroll := len(contentLines) - availableHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollOffset > maxScroll {
		m.scrollOffset = maxScroll
	}
	// Extract the visible portion.
	visibleContent := strings.Join(contentLines[m.scrollOffset:min(m.scrollOffset+availableHeight, len(contentLines))], "\n")

	return staticPart + visibleContent
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// -------------------------
// Main
// -------------------------

func main() {
	facetFlag := flag.Int("facet", 0, "Facet column (1-indexed) to display; 0 for all facets")
	statsFlag := flag.Bool("stats", false, "Display mean and stdev instead of a full histogram")
	flag.Parse()

	m := &model{
		facetsData:    make(map[int]map[string][]float64),
		totalLogCount: 0,
		startTime:     time.Now(),
		facet:         *facetFlag,
		stats:         *statsFlag,
		lines:         make(chan string, 100),
		// Defaults for window dimensions; they will be updated on WindowSizeMsg.
		winWidth:  80,
		winHeight: 24,
		// String counting mode
		stringValues: make(map[string]int),
		countStrings: true, // Always count strings for any input
		// Navigation
		facetPositions:  make(map[string][2]int),
		activeFacetKeys: make([]string, 0),
		// Grid dimensions
		gridColumns: 0,
		gridRows:    0,
		// Pinning feature
		pinnedFacets:       make(map[string]bool),
		pinnedFacetsColumn: make(map[string]int),
		filteredData:       make(map[int]map[string][]float64),
		isFiltered:         false,
		// Store original lines
		storedLines: make([]string, 0),
	}

	p := tea.NewProgram(m)
	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
