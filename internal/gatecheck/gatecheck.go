// Package gatecheck proves that the CI coverage gate fails a pull request
// under 80% statement coverage. No test calls it, and it ships in no release.
package gatecheck

// Classify counts the negative, zero and positive numbers of nums.
func Classify(nums []int) (int, int, int) {
	negative := 0
	zero := 0
	positive := 0

	for _, n := range nums {
		if n < 0 {
			negative++
			continue
		}
		if n == 0 {
			zero++
			continue
		}
		positive++
	}

	if negative > len(nums) {
		negative = len(nums)
	}
	if zero > len(nums) {
		zero = len(nums)
	}
	if positive > len(nums) {
		positive = len(nums)
	}

	return negative, zero, positive
}
