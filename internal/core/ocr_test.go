package core

import (
	"os"
	"testing"
)

const receiptText = `  CORNER MARKET
123 MAIN ST
ANYTOWN FL 00000
11/20/2026 14:32
MILK 2%            3.49
BREAD              2.99
EGGS DOZEN         4.19
SUBTOTAL          10.67
TAX                 0.75
TOTAL             11.42
VISA ****0000     11.42
YOU SAVED          1.00
`

func TestParseReceipt(t *testing.T) {
	g := ParseReceipt(receiptText)
	if g.Merchant != "CORNER MARKET" || g.Date != "2026-11-20" || g.TotalCents != 1142 || !g.Confident {
		t.Errorf("%+v", g)
	}
	g2 := ParseReceipt("Coffee Spot\nNov 3, 2026\nLatte 4.50\nMuffin 3.25\nAmount Due\n$7.75\n")
	if g2.TotalCents != 775 || g2.Date != "2026-11-03" {
		t.Errorf("%+v", g2)
	}
	g3 := ParseReceipt("blurry\n12.00 8.99")
	if g3.Confident || g3.TotalCents != 1200 {
		t.Errorf("guess without a total line should not be confident: %+v", g3)
	}
}

func TestOCRRealImage(t *testing.T) {
	if !OCRAvailable() {
		t.Skip("tesseract not installed here")
	}
	b, err := os.ReadFile("testdata/receipt.png")
	if err != nil {
		t.Skip("no sample image")
	}
	text, err := ReadImageText(b, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if g := ParseReceipt(text); g.TotalCents == 0 {
		t.Errorf("nothing read: %q", text)
	}
}
