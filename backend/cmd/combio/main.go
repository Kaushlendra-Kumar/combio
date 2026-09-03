package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kaushlendra/combio/backend/internal/cli"
	"github.com/kaushlendra/combio/backend/internal/scenario"
)

const usage = `COMBIO — local table

  go run ./cmd/combio play                    play locally, pass-the-device
  go run ./cmd/combio play --debug            same, with all hidden cards shown
  go run ./cmd/combio play --seed 42          deterministic deal
  go run ./cmd/combio play --no-clear         never clear the screen (piped input)

  go run ./cmd/combio scenarios               list the deterministic scenarios
  go run ./cmd/combio scenario <name>         run one scenario transcript
  go run ./cmd/combio scenario all            run every scenario

  go run ./cmd/combio replay --seed 7         bot-only game, fully deterministic
      --players N   (2-8, default 4)
      --cards N     (4/6/8, default 4)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "play":
		play(os.Args[2:])
	case "scenario":
		runScenario(os.Args[2:])
	case "scenarios", "list":
		for _, n := range scenario.Names() {
			s, _ := scenario.Find(n)
			fmt.Printf("  %-22s %s\n", n, s.Desc)
		}
	case "replay":
		replay(os.Args[2:])
	default:
		fmt.Print(usage)
		os.Exit(1)
	}
}

func play(args []string) {
	fs := flag.NewFlagSet("play", flag.ExitOnError)
	debug := fs.Bool("debug", false, "show every hidden card")
	noClear := fs.Bool("no-clear", false, "do not clear the terminal")
	seed := fs.Int64("seed", 0, "deterministic seed (0 asks)")
	_ = fs.Parse(args)

	t := cli.NewIO(*noClear)
	s, ok := cli.Setup(t, *debug, *seed)
	if !ok {
		os.Exit(1)
	}
	s.Run()
}

func runScenario(args []string) {
	if len(args) == 0 {
		fmt.Println("name required; try: scenarios")
		os.Exit(1)
	}
	if args[0] == "all" {
		failed := 0
		for _, sc := range scenario.All() {
			if err := scenario.Run(os.Stdout, sc); err != nil {
				fmt.Printf("  ✗ %v\n", err)
				failed++
			}
		}
		fmt.Printf("\n%d scenario(s) run, %d failed\n", len(scenario.All()), failed)
		if failed > 0 {
			os.Exit(1)
		}
		return
	}
	sc, ok := scenario.Find(args[0])
	if !ok {
		fmt.Printf("unknown scenario %q. known: %s\n", args[0], strings.Join(scenario.Names(), ", "))
		os.Exit(1)
	}
	if err := scenario.Run(os.Stdout, sc); err != nil {
		fmt.Printf("  ✗ %v\n", err)
		os.Exit(1)
	}
}

func replay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	seed := fs.Int64("seed", 1, "deterministic seed")
	players := fs.Int("players", 4, "number of players (2-8)")
	cards := fs.Int("cards", 4, "cards per player (4/6/8)")
	debug := fs.Bool("debug", false, "show every hidden card")
	_ = fs.Parse(args)

	out, err := cli.Replay(*seed, *players, *cards, *debug)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Print(out)
}
