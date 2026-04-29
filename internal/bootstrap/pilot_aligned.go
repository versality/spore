package bootstrap

import (
	"fmt"

	"github.com/versality/spore/internal/align"
)

func detectPilotAligned(root string) (string, error) {
	p, err := align.Resolve(root)
	if err != nil {
		return "", err
	}
	c, err := align.LoadCriteria(root)
	if err != nil {
		return "", err
	}
	s, err := align.Read(p, c)
	if err != nil {
		return "", err
	}
	if !s.Met() {
		var missing []string
		if s.Notes < c.RequiredNotes {
			missing = append(missing, fmt.Sprintf("%d/%d notes", s.Notes, c.RequiredNotes))
		}
		if s.Promoted < c.RequiredPromoted {
			missing = append(missing, fmt.Sprintf("%d/%d promoted", s.Promoted, c.RequiredPromoted))
		}
		if !s.Flipped {
			missing = append(missing, "operator has not run `spore align flip`")
		}
		return "", fmt.Errorf("alignment incomplete: %v", missing)
	}
	return fmt.Sprintf("%d notes, %d promoted, flipped", s.Notes, s.Promoted), nil
}
