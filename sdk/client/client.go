// Package client is the programmatic entrypoint for starting and inspecting
// Chronos workflows over gRPC.
package client

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/sdk/converter"
	"github.com/AymanYouss/chronos-engine/sdk/workflow"
)

// Client is a connection to the Chronos control plane.
type Client struct {
	conn *grpc.ClientConn
	svc  commonv1.WorkflowServiceClient
}

// Dial connects to the control plane at the given host:port.
func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return &Client{conn: conn, svc: commonv1.NewWorkflowServiceClient(conn)}, nil
}

// Service exposes the raw gRPC client (used by the worker).
func (c *Client) Service() commonv1.WorkflowServiceClient { return c.svc }

// Close releases the connection.
func (c *Client) Close() error { return c.conn.Close() }

// StartWorkflowOptions configures a workflow start.
type StartWorkflowOptions struct {
	ID           string
	WorkflowType string
	TaskQueue    string
	Input        any
	RetryPolicy  *workflow.RetryPolicy
}

// StartWorkflowResult reports the outcome of a start request.
type StartWorkflowResult struct {
	WorkflowID     string
	RunID          string
	AlreadyStarted bool
}

// StartWorkflow enqueues a new workflow execution.
func (c *Client) StartWorkflow(ctx context.Context, opts StartWorkflowOptions) (*StartWorkflowResult, error) {
	input, err := converter.Encode(opts.Input)
	if err != nil {
		return nil, fmt.Errorf("encode input: %w", err)
	}
	if opts.TaskQueue == "" {
		opts.TaskQueue = "default"
	}
	req := &commonv1.StartWorkflowRequest{
		WorkflowId:   opts.ID,
		WorkflowType: opts.WorkflowType,
		TaskQueue:    opts.TaskQueue,
		Input:        input,
	}
	if opts.RetryPolicy != nil {
		req.RetryPolicy = opts.RetryPolicy.ToProto()
	}
	resp, err := c.svc.StartWorkflow(ctx, req)
	if err != nil {
		return nil, err
	}
	return &StartWorkflowResult{
		WorkflowID:     resp.WorkflowId,
		RunID:          resp.RunId,
		AlreadyStarted: resp.AlreadyStarted,
	}, nil
}

// SignalWorkflow delivers a signal to a running workflow.
func (c *Client) SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, input any) error {
	payload, err := converter.Encode(input)
	if err != nil {
		return fmt.Errorf("encode signal: %w", err)
	}
	_, err = c.svc.SignalWorkflow(ctx, &commonv1.SignalWorkflowRequest{
		WorkflowId: workflowID,
		RunId:      runID,
		SignalName: signalName,
		Input:      payload,
	})
	return err
}

// DescribeWorkflow returns execution metadata.
func (c *Client) DescribeWorkflow(ctx context.Context, workflowID, runID string) (*commonv1.WorkflowExecution, error) {
	resp, err := c.svc.DescribeWorkflow(ctx, &commonv1.DescribeWorkflowRequest{WorkflowId: workflowID, RunId: runID})
	if err != nil {
		return nil, err
	}
	return resp.Execution, nil
}

// GetHistory returns the full event history for a run.
func (c *Client) GetHistory(ctx context.Context, workflowID, runID string) ([]*commonv1.HistoryEvent, error) {
	resp, err := c.svc.GetWorkflowHistory(ctx, &commonv1.GetWorkflowHistoryRequest{WorkflowId: workflowID, RunId: runID})
	if err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// ListWorkflows returns executions filtered by status.
func (c *Client) ListWorkflows(ctx context.Context, status commonv1.WorkflowStatus, pageSize int32) ([]*commonv1.WorkflowExecution, error) {
	resp, err := c.svc.ListWorkflows(ctx, &commonv1.ListWorkflowsRequest{StatusFilter: status, PageSize: pageSize})
	if err != nil {
		return nil, err
	}
	return resp.Executions, nil
}
