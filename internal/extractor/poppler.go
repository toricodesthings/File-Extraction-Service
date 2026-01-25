package extractor

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

func PageCount(ctx context.Context, pdfPath string) (int, error) {
	cmd := exec.CommandContext(ctx, "pdfinfo", pdfPath)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	re := regexp.MustCompile(`(?m)^Pages:\s+(\d+)\s*$`)
	m := re.FindStringSubmatch(string(out))
	if len(m) != 2 {
		return 0, fmt.Errorf("pdfinfo: pages not found")
	}
	return strconv.Atoi(m[1])
}

func TextForPage(ctx context.Context, pdfPath string, page int) (string, error) {
	cmd := exec.CommandContext(ctx,
		"pdftotext",
		"-f", strconv.Itoa(page),
		"-l", strconv.Itoa(page),
		"-layout",
		pdfPath,
		"-",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
