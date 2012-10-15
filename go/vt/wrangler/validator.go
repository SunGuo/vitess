// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zkwrangler

import (
	"fmt"
	"net"
	"path"
	"strings"
	"sync"
	"time"

	"code.google.com/p/vitess/go/relog"
	rpc "code.google.com/p/vitess/go/rpcplus"
	vtrpc "code.google.com/p/vitess/go/vt/rpc"
	tm "code.google.com/p/vitess/go/vt/tabletmanager"
	"code.google.com/p/vitess/go/zk"
)

// As with all distributed systems, things can skew. These functions
// explore data in zookeeper and attempt to square that with reality.
//
// Given the node counts are usually large, this work should be done
// with as much parallelism as is viable.
//
// This may eventually move into a separate package.

type vresult struct {
	zkPath string
	err    error
}

// Validate a whole zk tree
func (wr *Wrangler) Validate(zkKeyspacesPath string, pingTablets bool) error {
	// Results from various actions feed here.
	results := make(chan vresult, 16)
	wg := sync.WaitGroup{}

	// Validate all tablets in all cells, even if they are not discoverable
	// by the replication graph.
	replicationPaths, err := zk.ChildrenRecursive(wr.zconn, zkKeyspacesPath)
	if err != nil {
		results <- vresult{zkKeyspacesPath, err}
	} else {
		cellSet := make(map[string]bool, 16)
		for _, p := range replicationPaths {
			p := path.Join(zkKeyspacesPath, p)
			if tm.IsTabletReplicationPath(p) {
				cell, _ := tm.ParseTabletReplicationPath(p)
				cellSet[cell] = true
			}
		}

		for cell, _ := range cellSet {
			zkTabletsPath := path.Join("/zk", cell, tm.VtSubtree(zkKeyspacesPath), "tablets")
			tabletUids, _, err := wr.zconn.Children(zkTabletsPath)
			if err != nil {
				results <- vresult{zkTabletsPath, err}
			} else {
				for _, tabletUid := range tabletUids {
					tabletPath := path.Join(zkTabletsPath, tabletUid)
					wg.Add(1)
					go func() {
						results <- vresult{tabletPath, tm.Validate(wr.zconn, tabletPath, "")}
						wg.Done()
					}()
				}
			}
		}
	}

	// Validate replication graph by traversing each keyspace and then each shard.
	keyspaces, _, err := wr.zconn.Children(zkKeyspacesPath)
	if err != nil {
		results <- vresult{zkKeyspacesPath, err}
	} else {
		for _, keyspace := range keyspaces {
			zkShardsPath := path.Join(zkKeyspacesPath, keyspace, "shards")
			shards, _, err := wr.zconn.Children(zkShardsPath)
			if err != nil {
				results <- vresult{zkShardsPath, err}
			}
			for _, shard := range shards {
				zkShardPath := path.Join(zkShardsPath, shard)
				wg.Add(1)
				go func() {
					wr.validateShard(zkShardPath, pingTablets, results)
					wg.Done()
				}()
			}
		}
	}

	timer := time.NewTimer(wr.actionTimeout)
	someErrors := false
	done := make(chan bool, 1)
	go func() {
		wg.Wait()
		done <- true
	}()

	for {
		select {
		case <-timer.C:
			return fmt.Errorf("timed out during validate")
		case vd := <-results:
			relog.Info("checking %v", vd.zkPath)
			if vd.err != nil {
				someErrors = true
				relog.Error("%v: %v", vd.zkPath, vd.err)
			}
		case <-done:
			if someErrors {
				return fmt.Errorf("some validation errors - see log")
			}
			return nil
		}
	}

	panic("unreachable")
}

func (wr *Wrangler) validateShard(zkShardPath string, pingTablets bool, results chan<- vresult) {
	shardInfo, err := tm.ReadShard(wr.zconn, zkShardPath)
	if err != nil {
		results <- vresult{zkShardPath, err}
		return
	}

	aliases, err := tm.FindAllTabletAliasesInShard(wr.zconn, zkShardPath)
	if err != nil {
		results <- vresult{zkShardPath, err}
	}

	shardTablets := make([]string, 0, 16)
	for _, alias := range aliases {
		shardTablets = append(shardTablets, tm.TabletPathForAlias(alias))
	}

	tabletMap, _ := GetTabletMap(wr.zconn, shardTablets)

	var masterAlias tm.TabletAlias
	for _, alias := range aliases {
		zkTabletPath := tm.TabletPathForAlias(alias)
		tabletInfo, ok := tabletMap[zkTabletPath]
		if !ok {
			results <- vresult{zkTabletPath, fmt.Errorf("tablet not found in map: %v", zkTabletPath)}
			continue
		}
		if tabletInfo.Parent.Uid == tm.NO_TABLET {
			if masterAlias.Cell != "" {
				results <- vresult{zkTabletPath, fmt.Errorf("%v: already has a master %v", zkTabletPath, masterAlias)}
			} else {
				masterAlias = alias
			}
		}
	}

	if masterAlias.Cell == "" {
		results <- vresult{zkShardPath, fmt.Errorf("no master for shard %v", zkShardPath)}
	} else if shardInfo.MasterAlias != masterAlias {
		results <- vresult{zkShardPath, fmt.Errorf("master mismatch for shard %v: found %v, expected %v", zkShardPath, masterAlias, shardInfo.MasterAlias)}
	}

	wg := sync.WaitGroup{}
	for _, alias := range aliases {
		zkTabletPath := tm.TabletPathForAlias(alias)
		zkTabletReplicationPath := path.Join(zkShardPath, masterAlias.String())
		if alias != masterAlias {
			zkTabletReplicationPath += "/" + alias.String()
		}
		wg.Add(1)
		go func() {
			results <- vresult{zkTabletReplicationPath, tm.Validate(wr.zconn, zkTabletPath, zkTabletReplicationPath)}
			wg.Done()
		}()
	}

	if pingTablets {
		wr.validateReplication(shardInfo, tabletMap, results)
		wr.pingTablets(tabletMap, results)
	}

	wg.Wait()
	return
}

func strInList(sl []string, s string) bool {
	for _, x := range sl {
		if x == s {
			return true
		}
	}
	return false
}

func (wr *Wrangler) validateReplication(shardInfo *tm.ShardInfo, tabletMap map[string]*tm.TabletInfo, results chan<- vresult) {
	masterTabletPath := tm.TabletPathForAlias(shardInfo.MasterAlias)
	masterTablet, ok := tabletMap[masterTabletPath]
	if !ok {
		err := fmt.Errorf("master not in tablet map: %v", masterTabletPath)
		results <- vresult{masterTabletPath, err}
		return
	}

	slaveAddrs, err := getSlaves(masterTablet.Tablet)
	if err != nil {
		results <- vresult{masterTabletPath, err}
		return
	}

	if len(slaveAddrs) == 0 {
		results <- vresult{masterTabletPath, fmt.Errorf("no slaves found: %v", masterTabletPath)}
		return
	}

	// Some addresses don't resolve in all locations, just use IP address
	if err != nil {
		results <- vresult{masterTabletPath, fmt.Errorf("resolve slaves failed: %v", err)}
		return
	}

	tabletIpMap := make(map[string]*tm.Tablet)
	for tabletPath, tablet := range tabletMap {
		ipAddr, err := tabletIp(tablet.MysqlAddr)
		if err != nil {
			results <- vresult{tabletPath, fmt.Errorf("bad mysql addr: %v %v %v", tablet.MysqlAddr, tabletPath, err)}
			continue
		}
		tabletIpMap[ipAddr] = tablet.Tablet
	}

	// See if every slave is in the replication graph.
	for _, slaveAddr := range slaveAddrs {
		if tabletIpMap[slaveAddr] == nil {
			results <- vresult{shardInfo.ShardPath(), fmt.Errorf("slave not in replication graph: %v", slaveAddr)}
		}
	}

	// See if every entry in the replication graph is connected to the master.
	for tabletPath, tablet := range tabletMap {
		if !tablet.IsReplicatingType() {
			continue
		}
		ipAddr, err := tabletIp(tablet.MysqlAddr)
		if err != nil {
			results <- vresult{tabletPath, fmt.Errorf("bad mysql addr: %v %v", tabletPath, err)}
		} else if !strInList(slaveAddrs, ipAddr) {
			results <- vresult{tabletPath, fmt.Errorf("slave not replicating: %v %v %q", tabletPath, ipAddr, slaveAddrs)}
		}
	}
}

func tabletIp(addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	ipAddrs, err := net.LookupHost(host)
	if err != nil {
		return "", err
	}
	return ipAddrs[0], nil
}

func (wr *Wrangler) pingTablets(tabletMap map[string]*tm.TabletInfo, results chan<- vresult) {
	wg := sync.WaitGroup{}
	for zkTabletPath, tabletInfo := range tabletMap {
		wg.Add(1)
		go func() {
			defer wg.Done()

			zkTabletPid := path.Join(tabletInfo.Path(), "pid")
			_, _, err := wr.zconn.Get(zkTabletPid)
			if err != nil {
				results <- vresult{zkTabletPath, fmt.Errorf("no pid node %v: %v %v", zkTabletPid, err, tabletInfo.Hostname())}
				return
			}

			actionPath, err := wr.ai.Ping(zkTabletPath)
			if err != nil {
				results <- vresult{zkTabletPath, fmt.Errorf("%v: %v %v", actionPath, err, tabletInfo.Hostname())}
				return
			}

			err = wr.ai.WaitForCompletion(actionPath, wr.actionTimeout)
			if err != nil {
				results <- vresult{zkTabletPath, fmt.Errorf("%v: %v %v", actionPath, err, tabletInfo.Hostname())}
			}
		}()
	}

	wg.Wait()
}

// Slaves come back as IP addrs, resolve to host names.
func resolveSlaveNames(addrs []string) (hostnames []string, err error) {
	hostnames = make([]string, len(addrs))
	for i, addr := range addrs {
		if net.ParseIP(addr) != nil {
			ipAddrs, err := net.LookupAddr(addr)
			if err != nil {
				return nil, err
			}
			addr = ipAddrs[0]
		}
		cname, err := net.LookupCNAME(addr)
		if err != nil {
			return nil, err
		}
		hostnames[i] = strings.TrimRight(cname, ".")
	}
	return
}

// Get list of slave ip addresses from the tablet.
func getSlaves(tablet *tm.Tablet) ([]string, error) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	callChan := make(chan *rpc.Call, 1)

	go func() {
		client, clientErr := rpc.DialHTTP("tcp", tablet.Addr)
		if clientErr != nil {
			callChan <- &rpc.Call{Error: fmt.Errorf("dial failed: %v", clientErr)}
			return
		}

		slaveList := new(tm.SlaveList)
		// Forward the message so we close the connection after the rpc is done.
		done := make(chan *rpc.Call, 1)
		client.Go("TabletManager.GetSlaves", vtrpc.NilRequest, slaveList, done)
		callChan <- <-done
		client.Close()
	}()

	select {
	case <-timer.C:
		return nil, fmt.Errorf("TabletManager.GetSlaves deadline exceeded %v", tablet.Addr)
	case call := <-callChan:
		if call.Error != nil {
			return nil, call.Error
		}
		return call.Reply.(*tm.SlaveList).Addrs, nil
	}

	panic("unreachable")
}