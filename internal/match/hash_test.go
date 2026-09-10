package match

import "testing"

// 黄金向量：算法一旦变更（会改变既有题库的 unique_hash 匹配）此测试必须显式更新。
const goldenVector = "2d331179688c8cfcb19294255b16b47693edaaf4afee09d3f77c4f18ff5e8239"

func TestUniqueHash_GoldenVector(t *testing.T) {
	got := UniqueHash("wealth . ", []string{"health . ", "value . ", "wealth . ", "ring . "})
	if got != goldenVector {
		t.Fatalf("golden vector changed: got %s want %s", got, goldenVector)
	}
}

func TestUniqueHash_OrderInsensitive(t *testing.T) {
	a := UniqueHash("stem", []string{"a", "b", "c", "d"})
	b := UniqueHash("stem", []string{"d", "c", "b", "a"})
	if a != b {
		t.Fatalf("hash must not depend on option order: %s vs %s", a, b)
	}
}

func TestUniqueHash_DistinctInputs(t *testing.T) {
	base := UniqueHash("stem", []string{"a", "b", "c", "d"})
	if UniqueHash("stem2", []string{"a", "b", "c", "d"}) == base {
		t.Fatal("different stems must hash differently")
	}
	if UniqueHash("stem", []string{"a", "b", "c", "e"}) == base {
		t.Fatal("different options must hash differently")
	}
}
