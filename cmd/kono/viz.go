package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/starwalkn/kono"
)

var vizCmd = &cobra.Command{
	Use:   "viz",
	Short: "Visualize gateway flows",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if cfgPath == "" {
			cfgPath = os.Getenv("KONO_CONFIG")
		}
		if cfgPath == "" {
			cfgPath = fallbackConfigPath
		}

		cfg, err := kono.LoadConfig(cfgPath)
		if err != nil {
			return err
		}

		v := newViz(cfg)
		v.render()

		return nil
	},
}

func init() {
	rootCmd.AddCommand(vizCmd)
}

const (
	labelColumnWidth  = 12 // Width of the "plugins" / "middlewares" label column.
	upstreamNameWidth = 20 // Minimum width for upstream name column alignment.
)

var (
	styleFaint = lipgloss.NewStyle().Faint(true)
	styleTree  = lipgloss.NewStyle().Faint(true)
	styleHost  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	stylePath  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	styleBadge = lipgloss.NewStyle().Padding(0, 1).Bold(true)

	methodColors = map[string]lipgloss.Color{
		"GET":     "34",  // green
		"POST":    "33",  // blue
		"PUT":     "136", // yellow
		"PATCH":   "166", // orange
		"DELETE":  "160", // red
		"HEAD":    "240", // gray
		"OPTIONS": "240",
	}

	styleStrategy = lipgloss.NewStyle().
			Foreground(lipgloss.Color("117")).
			Faint(true)

	stylePassthrough = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Bold(true)

	styleUpstream = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("111"))

	styleMeta = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	styleCB = lipgloss.NewStyle().
		Foreground(lipgloss.Color("203"))

	styleLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Width(labelColumnWidth)

	styleNames = lipgloss.NewStyle().
			Foreground(lipgloss.Color("183"))

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("255")).
			Padding(0, 1)

	styleHeaderMeta = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			Padding(0, 1)
)

type viz struct {
	cfg kono.Config
	sb  strings.Builder
}

func newViz(cfg kono.Config) *viz {
	return &viz{cfg: cfg}
}

func (v *viz) render() {
	routing := v.cfg.Gateway.Routing

	v.renderHeader(routing)
	fmt.Println()

	for i, f := range routing.Flows {
		v.renderFlow(f)

		if i < len(routing.Flows)-1 {
			fmt.Println()
		}
	}

	fmt.Println()
	fmt.Print(v.sb.String())
}

func (v *viz) renderHeader(routing kono.RoutingConfig) {
	count := fmt.Sprintf("%d flow", len(routing.Flows))
	if len(routing.Flows) != 1 {
		count += "s"
	}

	rl := "rate limit: off"
	if routing.RateLimiter.Enabled {
		rl = "rate limit: on"
	}

	fmt.Println()
	fmt.Println(
		"  "+styleHeader.Render("KONO"),
		styleHeaderMeta.Render(count+"  ·  "+rl),
	)
}

func (v *viz) renderFlow(f kono.FlowConfig) {
	method := v.methodBadge(f.Method)
	path := "  " + method + "  " + stylePath.Render(f.Path)

	if f.Passthrough {
		path += "  " + stylePassthrough.Render("⇢ passthrough")
	} else {
		path += "  " + styleStrategy.Render(v.strategyLabel(f))
	}

	fmt.Println(path)

	indent := "  "

	if names := collectNames(f.Plugins, func(p kono.PluginConfig) string { return p.Name }); names != "" {
		fmt.Println(indent + styleLabel.Render("plugins") + styleNames.Render(names))
	}

	if names := collectNames(f.Middlewares, func(m kono.MiddlewareConfig) string { return m.Name }); names != "" {
		fmt.Println(indent + styleLabel.Render("middlewares") + styleNames.Render(names))
	}

	if len(f.Plugins) > 0 || len(f.Middlewares) > 0 {
		fmt.Println(styleTree.Render("  │"))
	}

	for i, u := range f.Upstreams {
		last := i == len(f.Upstreams)-1
		v.renderUpstream(u, last)
	}
}

func (v *viz) renderUpstream(u kono.UpstreamConfig, last bool) {
	connector := "  ├─◉ "
	hostIndent := "  │   "
	if last {
		connector = "  └─◉ "
		hostIndent = "      "
	}

	name := styleUpstream.Render(leftPad(u.Name, upstreamNameWidth))
	meta := v.upstreamMeta(u)

	fmt.Println(styleTree.Render(connector) + name + "  " + meta)

	for i, host := range u.Hosts {
		hostLast := i == len(u.Hosts)-1
		tree := hostIndent + styleTree.Render("├ ")

		if hostLast {
			tree = hostIndent + styleTree.Render("└ ")
		}

		hostStr := strings.TrimSuffix(host, "/")
		if u.Path != "" {
			hostStr += "/" + strings.TrimPrefix(u.Path, "/")
		}

		line := tree + styleHost.Render(hostStr)

		if u.Method != "" {
			line = tree + v.methodBadge(u.Method) + " " + styleHost.Render(hostStr)
		}

		fmt.Println(line)
	}
}

func (v *viz) methodBadge(method string) string {
	color, ok := methodColors[method]
	if !ok {
		color = "240"
	}

	return styleBadge.
		Background(color).
		Foreground(lipgloss.Color("0")).
		Render(method)
}

func (v *viz) strategyLabel(f kono.FlowConfig) string {
	s := f.Aggregation.Strategy

	if f.Aggregation.BestEffort {
		s += "  best-effort"
	}

	if f.Aggregation.OnConflict != nil && f.Aggregation.OnConflict.Policy != "" {
		s += "  on-conflict:" + f.Aggregation.OnConflict.Policy
	}

	return s
}

func (v *viz) upstreamMeta(u kono.UpstreamConfig) string {
	var parts []string

	if mode := u.Policy.LoadBalancingConfig.Mode; mode != "" {
		parts = append(parts, mode)
	}

	if u.Timeout > 0 {
		parts = append(parts, u.Timeout.String())
	}

	if n := u.Policy.RetryConfig.MaxRetries; n > 0 {
		parts = append(parts, fmt.Sprintf("retry ×%d", n))
	}

	base := styleMeta.Render(strings.Join(parts, " · "))

	if u.Policy.CircuitBreakerConfig.Enabled {
		base += "  " + styleCB.Render("⚡CB")
	}

	return base
}

// collectNames builds a " · "-joined string of names from a slice using nameFunc.
func collectNames[T any](items []T, nameFunc func(T) string) string {
	if len(items) == 0 {
		return ""
	}

	names := make([]string, len(items))
	for i, item := range items {
		names[i] = nameFunc(item)
	}

	return strings.Join(names, styleFaint.Render(" · "))
}

// leftPad pads s with trailing spaces to ensure column alignment up to width.
func leftPad(s string, width int) string {
	if len(s) >= width {
		return s
	}

	return s + strings.Repeat(" ", width-len(s))
}
