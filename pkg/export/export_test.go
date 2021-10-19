// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package export

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	gax "github.com/googleapis/gax-go/v2"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/tsdb/record"
	"google.golang.org/api/option"
	monitoredres_pb "google.golang.org/genproto/googleapis/api/monitoredres"
	monitoring_pb "google.golang.org/genproto/googleapis/monitoring/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	empty_pb "google.golang.org/protobuf/types/known/emptypb"
)

func TestBatchAdd(t *testing.T) {
	b := newBatch(nil, 100)

	if !b.empty() {
		t.Fatalf("batch unexpectedly not empty")
	}
	// Add 99 samples per project across 10 projects. The batch should not be full at
	// any point and never be empty after adding the first sample.
	for i := 0; i < 10; i++ {
		for j := 0; j < 99; j++ {
			if b.full() {
				t.Fatalf("batch unexpectedly full")
			}
			b.add(&monitoring_pb.TimeSeries{
				Resource: &monitoredres_pb.MonitoredResource{
					Labels: map[string]string{
						KeyProjectID: fmt.Sprintf("project-%d", i),
					},
				},
			})
			if b.empty() {
				t.Fatalf("batch unexpectedly empty")
			}
		}
	}
	if b.full() {
		t.Fatalf("batch unexpectedly full")
	}

	// Adding one more sample to one of the projects should make the batch be full.
	b.add(&monitoring_pb.TimeSeries{
		Resource: &monitoredres_pb.MonitoredResource{
			Labels: map[string]string{
				KeyProjectID: fmt.Sprintf("project-%d", 5),
			},
		},
	})
	if !b.full() {
		t.Fatalf("batch unexpectedly not full")
	}
}

func TestBatchFillFromShardsAndSend(t *testing.T) {
	// Fill the batch from 100 shards with samples across 100 projects.
	var shards []*shard
	for i := 0; i < 100; i++ {
		shards = append(shards, newShard(10000))
	}
	for i := 0; i < 10000; i++ {
		shards[i%100].enqueue(uint64(i), &monitoring_pb.TimeSeries{
			Resource: &monitoredres_pb.MonitoredResource{
				Labels: map[string]string{
					KeyProjectID: fmt.Sprintf("project-%d", i%100),
				},
			},
		})
	}

	b := newBatch(nil, 101)

	for _, s := range shards {
		s.fill(b)

		if !s.pending {
			t.Fatalf("shard unexpectedly not pending after fill")
		}
	}

	var mtx sync.Mutex
	receivedSamples := 0

	// When sending the batch we should see the right number of samples and all shards we pass should
	// be notified at the end.
	sendOne := func(ctx context.Context, req *monitoring_pb.CreateTimeSeriesRequest, opts ...gax.CallOption) error {
		mtx.Lock()
		receivedSamples += len(req.TimeSeries)
		mtx.Unlock()
		return nil
	}
	b.send(context.Background(), shards, sendOne)

	if want := 10000; receivedSamples != want {
		t.Fatalf("unexpected number of received samples (want=%d, got=%d)", want, receivedSamples)
	}
	for _, s := range shards {
		if s.pending {
			t.Fatalf("shard unexpectedtly pending after send")
		}
	}
}

func createBatch(s string) []record.RefSample {
	batch := []record.RefSample{}
	for i := 1; i < 10; i++ {
		lset := labels.Labels{
			{Name: "project_id", Value: s + fmt.Sprint(i)},
			{Name: "location", Value: "nyc"}}
		s := record.RefSample{
			Ref: lset.Hash(),
			T:   int64(i),
			V:   float64(i),
		}
		batch = append(batch, s)
	}
	return batch
}

type testMetricService struct {
	monitoring_pb.MetricServiceServer // Inherit all interface methods
	TimeSeries                        []monitoring_pb.TimeSeries
}

func (srv *testMetricService) CreateTimeSeries(ctx context.Context, req *monitoring_pb.CreateTimeSeriesRequest) (*empty_pb.Empty, error) {
	for _, ts := range req.TimeSeries {
		srv.TimeSeries = append(srv.TimeSeries, *ts)
	}
	return &empty_pb.Empty{}, nil
}

func TestExport(t *testing.T) {
	srv := grpc.NewServer()
	listener := bufconn.Listen(1e6)

	metricServer := &testMetricService{}
	monitoring_pb.RegisterMetricServiceServer(srv, metricServer)

	go func() { srv.Serve(listener) }()
	defer srv.Stop()
	ctx := context.Background()
	bufDialer := func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}
	metricClient, err := monitoring.NewMetricClient(ctx,
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithInsecure()),
		option.WithGRPCDialOption(grpc.WithContextDialer(bufDialer)),
	)
	if err != nil {
		t.Fatalf("creating metric client failed: %s", err)
	}

	e, err := New(nil, nil, ExporterOpts{})
	if err != nil {
		t.Fatalf("Creating Exporter failed: %s", err)
	}
	e.metricClient = metricClient
	e.SetLabelsByIDFunc(func(i uint64) labels.Labels {
		return labels.Labels{
			{Name: "project_id", Value: "p" + fmt.Sprint(i)},
			{Name: "location", Value: "nyc"}}
	})

	exportCtx, cancelExport := context.WithCancel(context.Background())
	go func() { e.Run(exportCtx) }()

	successfulBatch := createBatch("successful-batch")
	e.Export(gaugeMetadata, createBatch("successful-batch"))

	cancelExport()
	// Delay to allow timeseries to be sent.
	timer := time.NewTimer(1 * time.Second)
	<-timer.C
	// This batch will be rejected by exporter.
	e.Export(gaugeMetadata, createBatch("reject-batch"))
	// Only the first batch should have been sent.
	if len(metricServer.TimeSeries) != len(successfulBatch) {
		t.Fatalf("Export didn't send all TimeSeries after shutdown. (want=%d, got=%d)", len(successfulBatch), len(metricServer.TimeSeries))
	}
}
