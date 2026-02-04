package ydylconsolesdk

import "context"

// Result 聚合 `/v1/result` 下的所有接口。
type Result struct {
	http *HTTPClient
}

func (r Result) GetDeploySummary(ctx context.Context) (*SummaryResultResponse, error) {
	var out SummaryResultResponse
	if err := r.http.get(ctx, "/v1/result/summary", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r Result) GetDeployPipelineProgress(ctx context.Context) (*PipeProgressResponse, error) {
	var out PipeProgressResponse
	if err := r.http.get(ctx, "/v1/result/pipeline-progress", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r Result) GetNodeDeploymentContracts(ctx context.Context) (*NodeDeploymentContractsResponse, error) {
	var out NodeDeploymentContractsResponse
	if err := r.http.get(ctx, "/v1/result/node-deployment-contracts", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r Result) GetOpNodeDeploymentContracts(ctx context.Context) (*OpNodeDeploymentContracts, error) {
	var out OpNodeDeploymentContracts
	if err := r.http.get(ctx, "/v1/result/node-deployment-contracts/op", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r Result) GetCdkNodeDeploymentContracts(ctx context.Context) (*CdkNodeDeploymentContracts, error) {
	var out CdkNodeDeploymentContracts
	if err := r.http.get(ctx, "/v1/result/node-deployment-contracts/cdk", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r Result) GetXjstNodeDeploymentContracts(ctx context.Context) (*XjstNodeDeploymentContracts, error) {
	var out XjstNodeDeploymentContracts
	if err := r.http.get(ctx, "/v1/result/node-deployment-contracts/xjst", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

