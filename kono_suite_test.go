package kono

import (
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/starwalkn/kono/internal/metric"
)

var testMetrics *metric.Metrics

func TestKono(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kono Suite")
}

var _ = BeforeSuite(func() {
	tm, err := metric.New()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "init test metrics: %v\n", err)
		os.Exit(1)
	}

	testMetrics = tm
})
