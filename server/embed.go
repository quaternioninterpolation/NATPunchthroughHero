package main

import (
	_ "embed"
)

// dashboardHTML contains the embedded admin dashboard.
// It is a single self-contained HTML file with inline CSS and JS.
//
//go:embed dashboard/index.html
var dashboardHTML []byte
