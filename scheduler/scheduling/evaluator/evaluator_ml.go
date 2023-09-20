/*
 *     Copyright 2023 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package evaluator

import (
	"context"
	"log"
	"math/big"
	"strconv"
	"strings"

	"github.com/montanaflynn/stats"

	"d7y.io/api/pkg/apis/inference/v1"
	logger "d7y.io/dragonfly/v2/internal/dflog"
	inferenceclient "d7y.io/dragonfly/v2/pkg/rpc/inference/client"
	"d7y.io/dragonfly/v2/scheduler/resource"
)

type evaluatorML struct {
	inferenceClient inferenceclient.V1
}

// WithInferenceClient sets the grpc client of inference.
func WithInferenceClient(client inferenceclient.V1) Option {
	return func(em *evaluatorML) {
		em.inferenceClient = client
	}
}

// Option is a functional option for configuring the announcer.
type Option func(em *evaluatorML)

func NewEvaluatorML(options ...Option) Evaluator {
	em := &evaluatorML{}
	for _, opt := range options {
		opt(em)
	}

	return em
}

func (em *evaluatorML) getIP(ip string) []int64 {
	parts := strings.Split(ip, ".")
	var input []int64
	for _, part := range parts {
		if i, err := strconv.ParseInt(part, 10, 64); err != nil {
			log.Fatal(err)
		} else {
			input = append(input, i)
		}
	}
	return input
}

func (em *evaluatorML) getIDC(idc string) []int64 {
	IDC := []string{"a", "b", "c", "d", "e"}
	intput := make([]int64, len(IDC))
	for i := range IDC {
		if idc == IDC[i] {
			intput[i] = 1
		} else {
			intput[i] = 0
		}
	}
	return intput
}

func (em *evaluatorML) getLocation(location string) []int64 {
	Location := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	intput := make([]int64, len(Location))
	for i := range Location {
		if location == Location[i] {
			intput[i] = 1
		} else {
			intput[i] = 0
		}
	}
	return intput
}

// The larger the value after evaluation, the higher the priority.
func (em *evaluatorML) Evaluate(parent *resource.Peer, child *resource.Peer, totalPieceCount int32) float64 {
	inferInputs := []*inference.ModelInferRequest_InferInputTensor{
		{
			Name:     "IP",
			Datatype: "INT32",
			Shape:    []int64{1, 4},
			Contents: &inference.InferTensorContents{Int64Contents: em.getIP(parent.Host.IP)},
		},
		{
			Name:     "IDC",
			Datatype: "INT32",
			Shape:    []int64{1, 5},
			Contents: &inference.InferTensorContents{Int64Contents: em.getIDC(parent.Host.Network.IDC)},
		},
		{
			Name:     "Location",
			Datatype: "INT32",
			Shape:    []int64{1, 10},
			Contents: &inference.InferTensorContents{Int64Contents: em.getLocation(parent.Host.Network.Location)},
		},
	}
	inferOutputs := []*inference.ModelInferRequest_InferRequestedOutputTensor{
		{
			Name: "OUTPUT0",
		},
	}
	inferRequest := inference.ModelInferRequest{
		ModelName:    "test",
		ModelVersion: "1",
		Inputs:       inferInputs,
		Outputs:      inferOutputs,
	}

	inferResponse, err := em.inferenceClient.ModelInfer(context.Background(), &inferRequest)
	if err != nil {
		log.Fatal(err)
	}
	return float64(inferResponse.RawOutputContents[0][0])
}

func (em *evaluatorML) IsBadNode(peer *resource.Peer) bool {
	if peer.FSM.Is(resource.PeerStateFailed) || peer.FSM.Is(resource.PeerStateLeave) || peer.FSM.Is(resource.PeerStatePending) ||
		peer.FSM.Is(resource.PeerStateReceivedTiny) || peer.FSM.Is(resource.PeerStateReceivedSmall) ||
		peer.FSM.Is(resource.PeerStateReceivedNormal) || peer.FSM.Is(resource.PeerStateReceivedEmpty) {
		peer.Log.Debugf("peer is bad node because peer status is %s", peer.FSM.Current())
		return true
	}

	// Determine whether to bad node based on piece download costs.
	costs := stats.LoadRawData(peer.PieceCosts())
	len := len(costs)
	// Peer has not finished downloading enough piece.
	if len < minAvailableCostLen {
		logger.Debugf("peer %s has not finished downloading enough piece, it can't be bad node", peer.ID)
		return false
	}

	lastCost := costs[len-1]
	mean, _ := stats.Mean(costs[:len-1]) // nolint: errcheck

	// Download costs does not meet the normal distribution,
	// if the last cost is twenty times more than mean, it is bad node.
	if len < normalDistributionLen {
		isBadNode := big.NewFloat(lastCost).Cmp(big.NewFloat(mean*20)) > 0
		logger.Debugf("peer %s mean is %.2f and it is bad node: %t", peer.ID, mean, isBadNode)
		return isBadNode
	}

	// Download costs satisfies the normal distribution,
	// last cost falling outside of three-sigma effect need to be adjusted parent,
	// refer to https://en.wikipedia.org/wiki/68%E2%80%9395%E2%80%9399.7_rule.
	stdev, _ := stats.StandardDeviation(costs[:len-1]) // nolint: errcheck
	isBadNode := big.NewFloat(lastCost).Cmp(big.NewFloat(mean+3*stdev)) > 0
	logger.Debugf("peer %s meet the normal distribution, costs mean is %.2f and standard deviation is %.2f, peer is bad node: %t",
		peer.ID, mean, stdev, isBadNode)
	return isBadNode
}
