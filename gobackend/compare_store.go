package main

// CompareJobStore is the shared state store for compare jobs.
//
// NOTE: Job files/results are still stored on local filesystem (TMP_ROOT). This store
// only addresses "status/paid/code_url" consistency across pods and restarts.
type CompareJobStore interface {
	Create(job *CompareJob) error
	Get(id string) (*CompareJob, bool, error)
	Update(id string, fn func(j *CompareJob)) (*CompareJob, bool, error)
}
