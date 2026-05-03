package kono

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
)

var _ = Describe("Aggregator", func() {
	var agg *defaultAggregator

	BeforeEach(func() {
		agg = &defaultAggregator{}
	})

	Describe("with a single response", func() {
		It("returns success", func() {
			result := agg.aggregate(
				nil,
				[]upstreamResponse{okResponse(`{"a":1}`)},
				aggregation{strategy: strategyArray},
				zap.NewNop(),
			)

			Expect(result.errors).To(BeEmpty())
			Expect(result.partial).To(BeFalse())
			jsonEqual(`{"a":1}`, result.data)
		})

		It("maps upstream error to client error", func() {
			result := agg.aggregate(
				nil,
				[]upstreamResponse{errResponse(upstreamTimeout)},
				aggregation{strategy: strategyArray},
				zap.NewNop(),
			)

			Expect(result.data).To(BeNil())
			Expect(result.partial).To(BeFalse())
			Expect(result.errors).To(ConsistOf(ClientErrUpstreamUnavailable))
		})

		It("handles nil body", func() {
			result := agg.aggregate(
				nil,
				[]upstreamResponse{{body: nil}},
				aggregation{strategy: strategyMerge},
				zap.NewNop(),
			)

			Expect(result.data).To(BeNil())
			Expect(result.errors).To(BeEmpty())
			Expect(result.partial).To(BeFalse())
		})
	})

	Describe("merge strategy", func() {
		var responses []upstreamResponse

		BeforeEach(func() {
			responses = []upstreamResponse{
				okResponse(`{"a":1,"b":2}`),
				okResponse(`{"b":99,"c":4}`),
			}
		})

		DescribeTable("conflict policies",
			func(policy conflictPolicy, expected string) {
				result := agg.aggregate(nil, responses, aggregation{
					strategy:       strategyMerge,
					conflictPolicy: policy,
				}, zap.NewNop())

				Expect(result.errors).To(BeEmpty())
				jsonEqual(expected, result.data)
			},
			Entry("overwrite uses last value", conflictPolicyOverwrite, `{"a":1,"b":99,"c":4}`),
			Entry("first preserves earliest value", conflictPolicyFirst, `{"a":1,"b":2,"c":4}`),
		)

		It("returns conflict error when policy is error", func() {
			result := agg.aggregate(nil, responses, aggregation{
				strategy:       strategyMerge,
				conflictPolicy: conflictPolicyError,
			}, zap.NewNop())

			Expect(result.data).To(BeNil())
			Expect(result.errors).To(ConsistOf(ClientErrValueConflict))
		})

		It("returns partial result when best effort and one upstream fails", func() {
			result := agg.aggregate(
				nil,
				[]upstreamResponse{
					okResponse(`{"a":1}`),
					errResponse(upstreamTimeout),
				},
				aggregation{
					strategy:       strategyMerge,
					bestEffort:     true,
					conflictPolicy: conflictPolicyOverwrite,
				},
				zap.NewNop(),
			)

			Expect(result.partial).To(BeTrue())
			Expect(result.errors).To(ConsistOf(ClientErrUpstreamUnavailable))
			jsonEqual(`{"a":1}`, result.data)
		})
	})

	Describe("array strategy", func() {
		It("aggregates responses into an array", func() {
			result := agg.aggregate(
				nil,
				[]upstreamResponse{
					okResponse(`{"x":1}`),
					okResponse(`{"y":2}`),
				},
				aggregation{strategy: strategyArray},
				zap.NewNop(),
			)

			Expect(result.errors).To(BeEmpty())
			Expect(result.partial).To(BeFalse())
			jsonEqual(`[{"x":1},{"y":2}]`, result.data)
		})

		It("skips failed upstream when best effort", func() {
			result := agg.aggregate(
				nil,
				[]upstreamResponse{
					okResponse(`{"x":1}`),
					errResponse(upstreamBadStatus),
				},
				aggregation{strategy: strategyArray, bestEffort: true},
				zap.NewNop(),
			)

			Expect(result.partial).To(BeTrue())
			Expect(result.errors).To(ConsistOf(ClientErrUpstreamError))
			jsonEqual(`[{"x":1}]`, result.data)
		})
	})

	Describe("namespace strategy", func() {
		It("groups responses by upstream name", func() {
			result := agg.aggregate(
				stubUpstreams("users", "orders"),
				[]upstreamResponse{
					okResponse(`{"id":1}`),
					okResponse(`{"total":99}`),
				},
				aggregation{strategy: strategyNamespace},
				zap.NewNop(),
			)

			Expect(result.errors).To(BeEmpty())
			jsonEqual(`{"users":{"id":1},"orders":{"total":99}}`, result.data)
		})

		It("omits failed upstream when best effort", func() {
			result := agg.aggregate(
				stubUpstreams("users", "orders"),
				[]upstreamResponse{
					okResponse(`{"id":1}`),
					errResponse(upstreamTimeout),
				},
				aggregation{strategy: strategyNamespace, bestEffort: true},
				zap.NewNop(),
			)

			Expect(result.partial).To(BeTrue())
			Expect(result.errors).To(HaveLen(1))

			var got map[string]any
			Expect(decodeJSONInto(result.data, &got)).To(Succeed())
			Expect(got).To(HaveKey("users"))
			Expect(got).ToNot(HaveKey("orders"))
		})
	})

	DescribeTable("mapUpstreamError",
		func(kind upstreamErrorKind, want ClientError) {
			got := agg.mapUpstreamError(&upstreamError{
				kind: kind,
				err:  errors.New("err"),
			})
			Expect(got).To(Equal(want))
		},
		Entry("timeout → upstream unavailable", upstreamTimeout, ClientErrUpstreamUnavailable),
		Entry("connection → upstream unavailable", upstreamConnection, ClientErrUpstreamUnavailable),
		Entry("bad status → upstream error", upstreamBadStatus, ClientErrUpstreamError),
		Entry("body too large → upstream body too large", upstreamBodyTooLarge, ClientErrUpstreamBodyTooLarge),
		Entry("internal → internal", upstreamInternal, ClientErrInternal),
		Entry("read error → internal", upstreamReadError, ClientErrInternal),
	)
})
