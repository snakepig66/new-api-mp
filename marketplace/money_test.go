package marketplace

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMulDivCeil(t *testing.T) {
	t.Run("exact division does not round up", func(t *testing.T) {
		got, err := mulDivCeil(1_000_000, 1, 1_000_000)
		require.NoError(t, err)
		assert.Equal(t, Micro(1), got)
	})

	t.Run("remainder rounds up", func(t *testing.T) {
		got, err := mulDivCeil(1, 1, 1_000_000)
		require.NoError(t, err)
		assert.Equal(t, Micro(1), got, "ceil(1/1_000_000) must round up to 1")

		got, err = mulDivCeil(3, 7, 2)
		require.NoError(t, err)
		assert.Equal(t, Micro(11), got, "ceil(21/2) = 11")
	})

	t.Run("zero operand yields zero", func(t *testing.T) {
		for _, tc := range [][3]int64{
			{0, 5, 3},
			{5, 0, 3},
			{0, 0, 3},
		} {
			got, err := mulDivCeil(tc[0], tc[1], tc[2])
			require.NoError(t, err, "mulDivCeil(%d, %d, %d)", tc[0], tc[1], tc[2])
			assert.Equal(t, Micro(0), got)
		}
	})

	t.Run("negative operand rejected", func(t *testing.T) {
		_, err := mulDivCeil(-1, 5, 3)
		assert.ErrorIs(t, err, ErrMoneyNegative)
		_, err = mulDivCeil(5, -1, 3)
		assert.ErrorIs(t, err, ErrMoneyNegative)
	})

	t.Run("non-positive divisor rejected", func(t *testing.T) {
		_, err := mulDivCeil(5, 5, 0)
		assert.ErrorIs(t, err, ErrMoneyOverflow)
		_, err = mulDivCeil(5, 5, -1)
		assert.ErrorIs(t, err, ErrMoneyOverflow)
	})

	t.Run("product not fitting uint64 quotient rejected", func(t *testing.T) {
		_, err := mulDivCeil(math.MaxInt64, math.MaxInt64, 1)
		assert.ErrorIs(t, err, ErrMoneyOverflow)
	})

	t.Run("result exceeding int64 rejected", func(t *testing.T) {
		// MaxInt64 * 2 = 2^64 - 2 fits the 128-bit intermediate (hi=0 < den),
		// but the quotient 2^64 - 2 does not fit int64.
		_, err := mulDivCeil(math.MaxInt64, 2, 1)
		assert.ErrorIs(t, err, ErrMoneyOverflow)
	})

	t.Run("large exact arithmetic", func(t *testing.T) {
		// (1<<40) * (1<<20) = 2^60 = 1152921504606846976;
		// ceil(2^60 / 1_000_000) = 1152921504607.
		got, err := mulDivCeil(1<<40, 1<<20, MicroPerMillionTokens)
		require.NoError(t, err)
		assert.Equal(t, Micro(1152921504607), got)
	})
}

func TestTokenCostMicro(t *testing.T) {
	t.Run("one token at one micro per million rounds up to one micro", func(t *testing.T) {
		got, err := TokenCostMicro(1, 1)
		require.NoError(t, err)
		assert.Equal(t, Micro(1), got, "buyer rule: any positive usage bills at least 1 micro")
	})

	t.Run("one token at full million price is exactly one micro", func(t *testing.T) {
		got, err := TokenCostMicro(1, MicroPerMillionTokens)
		require.NoError(t, err)
		assert.Equal(t, Micro(1), got)
	})

	t.Run("exact million tokens is exact", func(t *testing.T) {
		got, err := TokenCostMicro(1_000_000, 2_500_000)
		require.NoError(t, err)
		assert.Equal(t, Micro(2_500_000), got)
	})

	t.Run("negative tokens rejected", func(t *testing.T) {
		_, err := TokenCostMicro(-1, MicroPerMillionTokens)
		assert.ErrorIs(t, err, ErrMoneyNegative)
	})
}

func TestToolCostMicro(t *testing.T) {
	t.Run("exact multiplication", func(t *testing.T) {
		got, err := ToolCostMicro(3, 5_000)
		require.NoError(t, err)
		assert.Equal(t, Micro(15_000), got)
	})

	t.Run("zero count is free", func(t *testing.T) {
		got, err := ToolCostMicro(0, 5_000)
		require.NoError(t, err)
		assert.Equal(t, Micro(0), got)
	})

	t.Run("overflow rejected", func(t *testing.T) {
		_, err := ToolCostMicro(math.MaxInt64, 2)
		assert.ErrorIs(t, err, ErrMoneyOverflow)
	})

	t.Run("negative operands rejected", func(t *testing.T) {
		_, err := ToolCostMicro(-1, 5_000)
		assert.ErrorIs(t, err, ErrMoneyNegative)
		_, err = ToolCostMicro(1, -5_000)
		assert.ErrorIs(t, err, ErrMoneyNegative)
	})
}

func TestSplitPlatformFee(t *testing.T) {
	t.Run("round half up table", func(t *testing.T) {
		cases := []struct {
			debit      Micro
			wantFee    Micro
			wantCredit Micro
		}{
			{100, 7, 93},
			{50, 4, 46}, // 3.5 rounds up to 4
			{7, 0, 7},   // 0.49 rounds down to 0
			{8, 1, 7},   // 0.56 rounds up to 1
			{0, 0, 0},
		}
		for _, tc := range cases {
			fee, credit, err := SplitPlatformFee(tc.debit)
			require.NoError(t, err, "debit=%d", tc.debit)
			assert.Equal(t, tc.wantFee, fee, "debit=%d fee", tc.debit)
			assert.Equal(t, tc.wantCredit, credit, "debit=%d credit", tc.debit)
			assert.Equal(t, tc.debit, fee+credit, "debit=%d identity", tc.debit)
		}
	})

	t.Run("negative debit rejected", func(t *testing.T) {
		_, _, err := SplitPlatformFee(-1)
		assert.ErrorIs(t, err, ErrMoneyNegative)
	})

	t.Run("identity and bounds hold for 0..10000", func(t *testing.T) {
		for debit := Micro(0); debit <= 10_000; debit++ {
			fee, credit, err := SplitPlatformFee(debit)
			require.NoError(t, err, "debit=%d", debit)
			assert.Equal(t, debit, fee+credit, "debit=%d: fee+credit must equal debit", debit)
			assert.GreaterOrEqual(t, fee, Micro(0), "debit=%d: fee must be non-negative", debit)
			assert.LessOrEqual(t, fee, debit, "debit=%d: fee must not exceed debit", debit)
		}
	})

	t.Run("overflow guard boundary", func(t *testing.T) {
		const maxSafe = (math.MaxInt64 - 50) / PlatformFeePercent

		fee, credit, err := SplitPlatformFee(Micro(maxSafe))
		require.NoError(t, err)
		assert.Equal(t, Micro(maxSafe), fee+credit, "identity must hold at the guard boundary")

		_, _, err = SplitPlatformFee(Micro(maxSafe + 1))
		assert.ErrorIs(t, err, ErrMoneyOverflow)
	})
}
