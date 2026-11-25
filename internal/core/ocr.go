package core

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// OCRAvailable reports whether the receipt reader can run here (Tesseract installed; the Docker image has it).
func OCRAvailable() bool {
	_, err := exec.LookPath("tesseract")
	return err == nil
}

// ReadImageText runs Tesseract on a photo or the first page of a PDF and returns the text.
func ReadImageText(data []byte, mime string) (string, error) {
	if !OCRAvailable() {
		return "", errors.New("the receipt reader needs Tesseract OCR; it is included in the Docker image (on Windows host mode install Tesseract and restart)")
	}
	dir, err := os.MkdirTemp("", "hhub-ocr-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	img := filepath.Join(dir, "in")
	switch mime {
	case "application/pdf":
		pdf := filepath.Join(dir, "in.pdf")
		if err := os.WriteFile(pdf, data, 0o600); err != nil {
			return "", err
		}
		if _, err := exec.LookPath("pdftoppm"); err != nil {
			return "", errors.New("reading PDFs needs pdftoppm (poppler); take a photo instead")
		}
		if out, err := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "200", "-f", "1", "-l", "1", "-singlefile", pdf, img).CombinedOutput(); err != nil {
			return "", errors.New("could not read the PDF: " + strings.TrimSpace(string(out)))
		}
		img += ".png"
	default:
		img += AllowedUpload[mime]
		if err := os.WriteFile(img, data, 0o600); err != nil {
			return "", err
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "tesseract", img, "stdout", "-l", "eng", "--psm", "4")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New("the reader failed: " + strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ReceiptGuess is what the reader found on a receipt.
type ReceiptGuess struct {
	Merchant   string `json:"merchant"`
	Date       string `json:"date"`
	TotalCents int64  `json:"total_cents"`
	Text       string `json:"text"`
	Confident  bool   `json:"confident"`
}

var (
	reRecMoney = regexp.MustCompile(`\$?\s?(\d{1,6}(?:,\d{3})*[.,]\d{2})\b`)
	reRecTotal = regexp.MustCompile(`(?i)\b(grand total|total due|amount due|balance due|total amount|amount paid|total|charged|visa|mastercard|debit)\b`)
	reRecSub   = regexp.MustCompile(`(?i)sub\s*-?\s*total|total\s+(savings|discount|items|tax)|you saved|tip guide`)
	reRecDates = []*regexp.Regexp{
		regexp.MustCompile(`\b(\d{1,2})[/-](\d{1,2})[/-](\d{4})\b`),
		regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`),
		regexp.MustCompile(`\b(\d{1,2})[/-](\d{1,2})[/-](\d{2})\b`),
		regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+(\d{1,2}),?\s+(\d{4})\b`),
	}
	reRecLetters = regexp.MustCompile(`[A-Za-z]`)
)

func recCents(s string) int64 {
	s = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(s), "$"), " ", "")
	if i := strings.LastIndexAny(s, ".,"); i == len(s)-3 {
		s = strings.NewReplacer(",", "", ".", "").Replace(s[:i]) + "." + s[i+1:]
	}
	c, err := ParseCents(s)
	if err != nil {
		return 0
	}
	return c
}

// ParseReceipt guesses the store, date and total from receipt text. It never invents a value: anything it
// cannot find stays empty for the person to fill in.
func ParseReceipt(text string) ReceiptGuess {
	g := ReceiptGuess{Text: text}
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	// store: the first line with real words near the top
	for i, l := range lines {
		if i > 8 {
			break
		}
		l = strings.TrimSpace(l)
		letters := len(reRecLetters.FindAllString(l, -1))
		if letters >= 3 && letters*2 >= len(l) && len(l) <= 40 && !reRecTotal.MatchString(l) {
			g.Merchant = strings.Trim(l, " .,:-*#")
			break
		}
	}
	// date
	for _, l := range lines {
		for i, re := range reRecDates {
			m := re.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			var t time.Time
			var err error
			switch i {
			case 0:
				t, err = time.Parse("1/2/2006", m[1]+"/"+m[2]+"/"+m[3])
			case 1:
				t, err = time.Parse(DateFmt, m[1]+"-"+m[2]+"-"+m[3])
			case 2:
				t, err = time.Parse("1/2/06", m[1]+"/"+m[2]+"/"+m[3])
			case 3:
				t, err = time.Parse("Jan 2 2006", strings.Title(strings.ToLower(m[1][:3]))+" "+m[2]+" "+m[3])
			}
			if err == nil && t.Year() >= 2000 && t.Year() <= 2100 {
				g.Date = t.Format(DateFmt)
				break
			}
		}
		if g.Date != "" {
			break
		}
	}
	// total: the last amount on a "total" line (not a subtotal); else the largest amount
	best, bestRank := int64(0), 99
	ranks := map[string]int{"grand total": 0, "total due": 1, "amount due": 1, "balance due": 1, "total amount": 1, "amount paid": 2, "total": 3, "charged": 4, "visa": 5, "mastercard": 5, "debit": 6}
	for i, l := range lines {
		m := reRecTotal.FindStringSubmatch(l)
		if m == nil || reRecSub.MatchString(l) {
			continue
		}
		amts := reRecMoney.FindAllStringSubmatch(l, -1)
		if len(amts) == 0 && i+1 < len(lines) { // amount on the next line
			amts = reRecMoney.FindAllStringSubmatch(lines[i+1], -1)
		}
		if len(amts) == 0 {
			continue
		}
		c := recCents(amts[len(amts)-1][1])
		r := ranks[strings.ToLower(m[1])]
		if c > 0 && (r < bestRank || (r == bestRank && c >= best)) {
			best, bestRank = c, r
		}
	}
	if best == 0 {
		for _, m := range reRecMoney.FindAllStringSubmatch(text, -1) {
			if c := recCents(m[1]); c > best {
				best = c
			}
		}
		bestRank = 99
	}
	g.TotalCents = best
	g.Confident = best > 0 && bestRank < 99 && g.Date != ""
	return g
}
