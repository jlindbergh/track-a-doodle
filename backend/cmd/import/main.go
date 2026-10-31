// Command import batch-imports all GPX files in a directory for one user.
//
//	docker compose run --rm backend import -dir /samples
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "import:", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", "/samples", "directory to search (recursively) for .gpx files")
	email := flag.String("user", os.Getenv("DEV_USER_EMAIL"), "email of the user who will own the activities")
	allowDuplicates := flag.Bool("allow-duplicates", false,
		"also import files that look like duplicates of existing activities (exact re-imports are still skipped)")
	flag.Parse()

	if *email == "" {
		return errors.New("-user (or DEV_USER_EMAIL) is required")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is not set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	st, err := store.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	u, err := st.EnsureUserByEmail(ctx, *email)
	if err != nil {
		return err
	}
	im := importer.New(st)

	counts := map[importer.Status]int{}
	noTrackSports := map[string]int{}
	// Files that could not be stored are reported like other failures, and
	// the import goes on with the next file; they only change the exit status.
	// If the database is unavailable, the import stops after the summary.
	storeErrors := 0
	err = filepath.WalkDir(*dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !importer.Accepts(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		res, err := im.ImportGPX(ctx, u.ID, path, data, importer.Options{AllowDuplicates: *allowDuplicates})
		if err != nil {
			if ctx.Err() != nil || store.IsUnavailable(err) {
				return err
			}
			fmt.Fprintf(os.Stderr, "import: %s: %v\n", path, err)
			storeErrors++
		}
		counts[res.Status]++
		switch {
		case res.Error != "":
			fmt.Printf("%-18s %s: %s\n", res.Status, path, res.Error)
		case res.Status == importer.StatusNoGPSTrack:
			fmt.Printf("%-18s %s (%s)\n", res.Status, path, res.Activity.Sport)
			noTrackSports[res.Activity.Sport]++
		case res.Status == importer.StatusPossibleDuplicate:
			fmt.Printf("%-18s %s: matches %q started %s\n", res.Status, path,
				res.Existing.Name, res.Existing.StartedAt.Format(time.DateTime))
		default:
			fmt.Printf("%-18s %s\n", res.Status, path)
		}
		return nil
	})
	unavailable := store.IsUnavailable(err)
	if err != nil && !unavailable {
		return err
	}

	fmt.Printf("\n%s: %d imported, %d already imported, %d possible duplicates, %d without GPS track, %d failed\n",
		u.Email, counts[importer.StatusImported], counts[importer.StatusAlreadyImported],
		counts[importer.StatusPossibleDuplicate], counts[importer.StatusNoGPSTrack], counts[importer.StatusFailed])
	if n := counts[importer.StatusNoGPSTrack]; n > 0 {
		sports := make([]string, 0, len(noTrackSports))
		for sport, c := range noTrackSports {
			sports = append(sports, fmt.Sprintf("%s (%d)", sport, c))
		}
		slices.Sort(sports)
		fmt.Printf("%d not imported because they have no GPS track: %s\n", n, strings.Join(sports, ", "))
	}
	if counts[importer.StatusPossibleDuplicate] > 0 && !*allowDuplicates {
		fmt.Printf("Possible duplicates start within %s of an existing activity, with distance and duration within %g%%.\n"+
			"Run again with -allow-duplicates to import them anyway.\n",
			importer.DuplicateStartTolerance, importer.DuplicateRelativeTolerance*100)
	}
	if unavailable {
		return fmt.Errorf("stopped because the database is unavailable; run again once it is back, files already imported are skipped: %w", err)
	}
	if storeErrors > 0 {
		return fmt.Errorf("%d files could not be saved because of a database error (see above)", storeErrors)
	}
	return nil
}
