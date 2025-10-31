package conditions

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

func SetOrUpdate(conds []metav1.Condition, c metav1.Condition) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(conds)+1)
	replaced := false
	for _, ex := range conds {
		if ex.Type == c.Type {
			if ex.Status == c.Status {
				c.LastTransitionTime = ex.LastTransitionTime
			}
			out = append(out, c)
			replaced = true
		} else {
			out = append(out, ex)
		}
	}
	if !replaced {
		out = append(out, c)
	}
	return out
}
