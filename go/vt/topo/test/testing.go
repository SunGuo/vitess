// package test contains utilities to test topo.Server
// implementations. If you are testing your implementation, you will
// want to call CheckAll in your test method. For an example, look at
// the tests in github.com/youtube/vitess/go/vt/zktopo.
package test

import (
	"encoding/json"
	"testing"

	"github.com/youtube/vitess/go/vt/key"
	"github.com/youtube/vitess/go/vt/topo"
)

func newKeyRange(value string) key.KeyRange {
	_, result, err := topo.ValidateShardName(value)
	if err != nil {
		panic(err)
	}
	return result
}

func CheckKeyspace(t *testing.T, ts topo.Server) {
	keyspaces, err := ts.GetKeyspaces()
	if err != nil {
		t.Errorf("GetKeyspaces(empty): %v", err)
	}
	if len(keyspaces) != 0 {
		t.Errorf("len(GetKeyspaces()) != 0: %v", keyspaces)
	}

	if err := ts.CreateKeyspace("test_keyspace"); err != nil {
		t.Errorf("CreateKeyspace: %v", err)
	}
	if err := ts.CreateKeyspace("test_keyspace"); err != topo.ErrNodeExists {
		t.Errorf("CreateKeyspace(again) is not ErrNodeExists: %v", err)
	}

	keyspaces, err = ts.GetKeyspaces()
	if err != nil {
		t.Errorf("GetKeyspaces: %v", err)
	}
	if len(keyspaces) != 1 || keyspaces[0] != "test_keyspace" {
		t.Errorf("GetKeyspaces: want %v, got %v", []string{"test_keyspace"}, keyspaces)
	}

	if err := ts.CreateKeyspace("test_keyspace2"); err != nil {
		t.Errorf("CreateKeyspace: %v", err)
	}
	keyspaces, err = ts.GetKeyspaces()
	if err != nil {
		t.Errorf("GetKeyspaces: %v", err)
	}
	if len(keyspaces) != 2 || keyspaces[0] != "test_keyspace" || keyspaces[1] != "test_keyspace2" {
		t.Errorf("GetKeyspaces: want %v, got %v", []string{"test_keyspace", "test_keyspace2"}, keyspaces)
	}
}

func tabletEqual(left, right *topo.Tablet) (bool, error) {
	lj, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	rj, err := json.Marshal(right)
	if err != nil {
		return false, err
	}
	return string(lj) == string(rj), nil
}

func CheckTablet(t *testing.T, ts topo.Server) {
	cell := getLocalCell(t, ts)
	tablet := &topo.Tablet{
		Cell:     cell,
		Uid:      1,
		Parent:   topo.TabletAlias{},
		Addr:     "localhost:3333",
		Keyspace: "test_keyspace",
		Type:     topo.TYPE_MASTER,
		State:    topo.STATE_READ_WRITE,
		KeyRange: newKeyRange("-10"),
	}
	if err := ts.CreateTablet(tablet); err != nil {
		t.Errorf("CreateTablet: %v", err)
	}
	if err := ts.CreateTablet(tablet); err != topo.ErrNodeExists {
		t.Errorf("CreateTablet(again): %v", err)
	}

	if _, err := ts.GetTablet(topo.TabletAlias{cell, 666}); err != topo.ErrNoNode {
		t.Errorf("GetTablet(666): %v", err)
	}

	ti, err := ts.GetTablet(tablet.Alias())
	if err != nil {
		t.Errorf("GetTablet %v: %v", tablet.Alias(), err)
	}
	if eq, err := tabletEqual(ti.Tablet, tablet); err != nil {
		t.Errorf("cannot compare tablets: %v", err)
	} else if !eq {
		t.Errorf("put and got tablets are not identical:\n%#v\n%#v", tablet, ti.Tablet)
	}

	if _, err := ts.GetTabletsByCell("666"); err != topo.ErrNoNode {
		t.Errorf("GetTabletsByCell(666): %v", err)
	}

	inCell, err := ts.GetTabletsByCell(cell)
	if err != nil {
		t.Errorf("GetTabletsByCell: %v", err)
	}
	if len(inCell) != 1 || inCell[0] != tablet.Alias() {
		t.Errorf("GetTabletsByCell: want [%v], got %v", tablet.Alias(), inCell)
	}

	ti.State = topo.STATE_READ_ONLY
	if err := topo.UpdateTablet(ts, ti); err != nil {
		t.Errorf("UpdateTablet: %v", err)
	}

	ti, err = ts.GetTablet(tablet.Alias())
	if err != nil {
		t.Errorf("GetTablet %v: %v", tablet.Alias(), err)
	}
	if want := topo.STATE_READ_ONLY; ti.State != want {
		t.Errorf("ti.State: want %v, got %v", want, ti.State)
	}

	if err := ts.UpdateTabletFields(tablet.Alias(), func(t *topo.Tablet) error {
		t.State = topo.STATE_READ_WRITE
		return nil
	}); err != nil {
		t.Errorf("UpdateTabletFields: %v", err)
	}
	ti, err = ts.GetTablet(tablet.Alias())
	if err != nil {
		t.Errorf("GetTablet %v: %v", tablet.Alias(), err)
	}

	if want := topo.STATE_READ_WRITE; ti.State != want {
		t.Errorf("ti.State: want %v, got %v", want, ti.State)
	}

	if err := ts.DeleteTablet(tablet.Alias()); err != nil {
		t.Errorf("DeleteTablet: %v", err)
	}
	if err := ts.DeleteTablet(tablet.Alias()); err != topo.ErrNoNode {
		t.Errorf("DeleteTablet(again): %v", err)
	}

	if _, err := ts.GetTablet(tablet.Alias()); err != topo.ErrNoNode {
		t.Errorf("GetTablet: expected error, tablet was deleted: %v", err)
	}

}

func getLocalCell(t *testing.T, ts topo.Server) string {
	cells, err := ts.GetKnownCells()
	if err != nil {
		t.Fatalf("GetKnownCells: %v", err)
	}
	if len(cells) < 1 {
		t.Fatalf("provided topo.Server doesn't have enough cells (need at least 1): %v", cells)
	}
	return cells[0]
}

func CheckShard(t *testing.T, ts topo.Server) {
	if err := ts.CreateKeyspace("test_keyspace"); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	if err := topo.CreateShard(ts, "test_keyspace", "-10"); err != nil {
		t.Fatalf("CreateShard: %v", err)
	}
	if err := topo.CreateShard(ts, "test_keyspace", "-10"); err != topo.ErrNodeExists {
		t.Errorf("CreateShard called second time, got: %v", err)
	}

	if _, err := ts.GetShard("test_keyspace", "666"); err != topo.ErrNoNode {
		t.Errorf("GetShard(666): %v", err)
	}

	shardInfo, err := ts.GetShard("test_keyspace", "-10")
	if err != nil {
		t.Errorf("GetShard: %v", err)
	}
	if want := newKeyRange("-10"); shardInfo.KeyRange != want {
		t.Errorf("shardInfo.KeyRange: want %v, got %v", want, shardInfo.KeyRange)
	}
	master := topo.TabletAlias{Cell: "ny", Uid: 1}
	replica1 := topo.TabletAlias{Cell: "ny", Uid: 2}
	replica2 := topo.TabletAlias{Cell: "ny", Uid: 3}
	rdonly1 := topo.TabletAlias{Cell: "sj", Uid: 4}
	rdonly2 := topo.TabletAlias{Cell: "sj", Uid: 5}
	shardInfo.MasterAlias = master
	shardInfo.ReplicaAliases = []topo.TabletAlias{replica1, replica2}
	shardInfo.RdonlyAliases = []topo.TabletAlias{rdonly1, rdonly2}
	shardInfo.KeyRange = newKeyRange("-10")
	shardInfo.ServedTypes = []topo.TabletType{topo.TYPE_MASTER, topo.TYPE_REPLICA, topo.TYPE_RDONLY}
	shardInfo.SourceShards = []topo.SourceShard{
		topo.SourceShard{
			Uid:      1,
			Keyspace: "source_ks",
			Shard:    "08-10",
			KeyRange: newKeyRange("08-10"),
		},
	}

	if err := ts.UpdateShard(shardInfo); err != nil {
		t.Errorf("UpdateShard: %v", err)
	}

	shardInfo, err = ts.GetShard("test_keyspace", "-10")
	if err != nil {
		t.Errorf("GetShard: %v", err)
	}
	if shardInfo.MasterAlias != master {
		t.Errorf("after UpdateShard: shardInfo.MasterAlias got %v", shardInfo.MasterAlias)
	}
	if len(shardInfo.ReplicaAliases) != 2 || shardInfo.ReplicaAliases[0] != replica1 || shardInfo.ReplicaAliases[1] != replica2 {
		t.Errorf("after UpdateShard: shardInfo.ReplicaAliases got %v", shardInfo.ReplicaAliases)
	}
	if len(shardInfo.RdonlyAliases) != 2 || shardInfo.RdonlyAliases[0] != rdonly1 || shardInfo.RdonlyAliases[1] != rdonly2 {
		t.Errorf("after UpdateShard: shardInfo.RdonlyAliases got %v", shardInfo.RdonlyAliases)
	}
	if shardInfo.KeyRange != newKeyRange("-10") {
		t.Errorf("after UpdateShard: shardInfo.KeyRange got %v", shardInfo.KeyRange)
	}
	if len(shardInfo.ServedTypes) != 3 || shardInfo.ServedTypes[0] != topo.TYPE_MASTER || shardInfo.ServedTypes[1] != topo.TYPE_REPLICA || shardInfo.ServedTypes[2] != topo.TYPE_RDONLY {
		t.Errorf("after UpdateShard: shardInfo.ServedTypes got %v", shardInfo.ServedTypes)
	}
	if len(shardInfo.SourceShards) != 1 || shardInfo.SourceShards[0].Uid != 1 || shardInfo.SourceShards[0].Keyspace != "source_ks" || shardInfo.SourceShards[0].Shard != "08-10" || shardInfo.SourceShards[0].KeyRange != newKeyRange("08-10") {
		t.Errorf("after UpdateShard: shardInfo.SourceShards got %v", shardInfo.SourceShards)
	}

	shards, err := ts.GetShardNames("test_keyspace")
	if err != nil {
		t.Errorf("GetShardNames: %v", err)
	}
	if len(shards) != 1 || shards[0] != "-10" {
		t.Errorf(`GetShardNames: want [ "-10" ], got %v`, shards)
	}

	if _, err := ts.GetShardNames("test_keyspace666"); err != topo.ErrNoNode {
		t.Errorf("GetShardNames(666): %v", err)
	}

}

func CheckReplicationPaths(t *testing.T, ts topo.Server) {
	if _, err := ts.GetReplicationPaths("test_keyspace", "-10", "/"); err != topo.ErrNoNode {
		t.Errorf("GetReplicationPaths(bad shard): %v", err)
	}

	if err := ts.CreateKeyspace("test_keyspace"); err != nil {
		t.Errorf("CreateKeyspace: %v", err)
	}
	if err := topo.CreateShard(ts, "test_keyspace", "-10"); err != nil {
		t.Errorf("CreateShard: %v", err)
	}

	if paths, err := ts.GetReplicationPaths("test_keyspace", "-10", "/"); err != nil || len(paths) != 0 {
		t.Errorf("GetReplicationPaths(empty shard): %v, %v", err, paths)
	}
	if _, err := ts.GetReplicationPaths("test_keyspace", "-10", "/666"); err != topo.ErrNoNode {
		t.Errorf("GetReplicationPaths(non-existing path): %v", err)
	}

	if err := ts.CreateReplicationPath("test_keyspace", "-10", "/cell-0000000001"); err != nil {
		t.Errorf("CreateReplicationPath: %v", err)
	}
	if err := ts.CreateReplicationPath("test_keyspace", "-10", "/cell-0000000001"); err != topo.ErrNodeExists {
		t.Errorf("CreateReplicationPath(again): %v", err)
	}

	if paths, err := ts.GetReplicationPaths("test_keyspace", "-10", "/"); err != nil || len(paths) != 1 || paths[0].String() != "cell-0000000001" {
		t.Errorf("GetReplicationPaths(root): %v, %v", err, paths)
	}

	if err := ts.CreateReplicationPath("test_keyspace", "-10", "/cell-0000000001/cell-0000000002"); err != nil {
		t.Errorf("CreateReplicationPath(2): %v", err)
	}
	if err := ts.CreateReplicationPath("test_keyspace", "-10", "/cell-0000000001/cell-0000000003"); err != nil {
		t.Errorf("CreateReplicationPath(3): %v", err)
	}
	if paths, err := ts.GetReplicationPaths("test_keyspace", "-10", "/cell-0000000001"); err != nil || len(paths) != 2 || paths[0].String() != "cell-0000000002" || paths[1].String() != "cell-0000000003" {
		t.Errorf("GetReplicationPaths(master): %v, %v", err, paths)
	}

	if err := ts.DeleteReplicationPath("test_keyspace", "-10", "/cell-0000000001"); err != topo.ErrNotEmpty {
		t.Errorf("DeleteReplicationPath(master with slaves): %v", err)
	}
	if err := ts.DeleteReplicationPath("test_keyspace", "-10", "/cell-0000000001/cell-0000000002"); err != nil {
		t.Errorf("DeleteReplicationPath(slave1): %v", err)
	}
	if paths, err := ts.GetReplicationPaths("test_keyspace", "-10", "/cell-0000000001"); err != nil || len(paths) != 1 || paths[0].String() != "cell-0000000003" {
		t.Errorf("GetReplicationPaths(master): %v, %v", err, paths)
	}
	if err := ts.DeleteReplicationPath("test_keyspace", "-10", "/cell-0000000001/cell-0000000003"); err != nil {
		t.Errorf("DeleteReplicationPath(slave2): %v", err)
	}
	if paths, err := ts.GetReplicationPaths("test_keyspace", "-10", "/cell-0000000001"); err != nil || len(paths) != 0 {
		t.Errorf("GetReplicationPaths(master): %v, %v", err, paths)
	}
	if err := ts.DeleteReplicationPath("test_keyspace", "-10", "/cell-0000000001"); err != nil {
		t.Errorf("DeleteReplicationPath(master): %v", err)
	}
	if paths, err := ts.GetReplicationPaths("test_keyspace", "-10", "/"); err != nil || len(paths) != 0 {
		t.Errorf("GetReplicationPaths(root): %v, %v", err, paths)
	}
}

func CheckServingGraph(t *testing.T, ts topo.Server) {

	// test individual cell/keyspace/shard/type entries
	if _, err := ts.GetSrvTabletTypesPerShard("cell", "test_keyspace", "-10"); err != topo.ErrNoNode {
		t.Errorf("GetSrvTabletTypesPerShard(invalid): %v", err)
	}
	if _, err := ts.GetSrvTabletType("cell", "test_keyspace", "-10", topo.TYPE_MASTER); err != topo.ErrNoNode {
		t.Errorf("GetSrvTabletType(invalid): %v", err)
	}

	vtnsAddrs := topo.VtnsAddrs{
		Entries: []topo.VtnsAddr{
			topo.VtnsAddr{
				Uid:  1,
				Host: "host1",
				Port: 1,
			},
		},
	}

	if err := ts.UpdateSrvTabletType("cell", "test_keyspace", "-10", topo.TYPE_MASTER, &vtnsAddrs); err != nil {
		t.Errorf("UpdateSrvTabletType(master): %v", err)
	}
	if types, err := ts.GetSrvTabletTypesPerShard("cell", "test_keyspace", "-10"); err != nil || len(types) != 1 || types[0] != topo.TYPE_MASTER {
		t.Errorf("GetSrvTabletTypesPerShard(1): %v %v", err, types)
	}
	if addrs, err := ts.GetSrvTabletType("cell", "test_keyspace", "-10", topo.TYPE_MASTER); err != nil || len(addrs.Entries) != 1 || addrs.Entries[0].Uid != 1 {
		t.Errorf("GetSrvTabletType(1): %v %v", err, addrs)
	}

	if err := ts.UpdateTabletEndpoint("cell", "test_keyspace", "-10", topo.TYPE_REPLICA, &topo.VtnsAddr{Uid: 2, Host: "host2", Port: 2}); err != nil {
		t.Errorf("UpdateTabletEndpoint(invalid): %v", err)
	}
	if err := ts.UpdateTabletEndpoint("cell", "test_keyspace", "-10", topo.TYPE_MASTER, &topo.VtnsAddr{Uid: 1, Host: "host2", Port: 2}); err != nil {
		t.Errorf("UpdateTabletEndpoint(master): %v", err)
	}
	if addrs, err := ts.GetSrvTabletType("cell", "test_keyspace", "-10", topo.TYPE_MASTER); err != nil || len(addrs.Entries) != 1 || addrs.Entries[0].Uid != 1 {
		t.Errorf("GetSrvTabletType(2): %v %v", err, addrs)
	}
	if err := ts.UpdateTabletEndpoint("cell", "test_keyspace", "-10", topo.TYPE_MASTER, &topo.VtnsAddr{Uid: 3, Host: "host3", Port: 3}); err != nil {
		t.Errorf("UpdateTabletEndpoint(master): %v", err)
	}
	if addrs, err := ts.GetSrvTabletType("cell", "test_keyspace", "-10", topo.TYPE_MASTER); err != nil || len(addrs.Entries) != 2 {
		t.Errorf("GetSrvTabletType(2): %v %v", err, addrs)
	}

	if err := ts.DeleteSrvTabletType("cell", "test_keyspace", "-10", topo.TYPE_REPLICA); err != topo.ErrNoNode {
		t.Errorf("DeleteSrvTabletType(unknown): %v", err)
	}
	if err := ts.DeleteSrvTabletType("cell", "test_keyspace", "-10", topo.TYPE_MASTER); err != nil {
		t.Errorf("DeleteSrvTabletType(master): %v", err)
	}

	// test cell/keyspace/shard entries (SrvShard)
	srvShard := topo.SrvShard{
		ServedTypes: []topo.TabletType{topo.TYPE_MASTER},
	}
	if err := ts.UpdateSrvShard("cell", "test_keyspace", "-10", &srvShard); err != nil {
		t.Errorf("UpdateSrvShard(1): %v", err)
	}
	if _, err := ts.GetSrvShard("cell", "test_keyspace", "666"); err != topo.ErrNoNode {
		t.Errorf("GetSrvShard(invalid): %v", err)
	}
	if s, err := ts.GetSrvShard("cell", "test_keyspace", "-10"); err != nil || len(s.ServedTypes) != 1 || s.ServedTypes[0] != topo.TYPE_MASTER {
		t.Errorf("GetSrvShard(valid): %v", err)
	}

	// test cell/keyspace entries (SrvKeyspace)
	srvKeyspace := topo.SrvKeyspace{
		TabletTypes: []topo.TabletType{topo.TYPE_MASTER},
	}
	if err := ts.UpdateSrvKeyspace("cell", "test_keyspace", &srvKeyspace); err != nil {
		t.Errorf("UpdateSrvKeyspace(1): %v", err)
	}
	if _, err := ts.GetSrvKeyspace("cell", "test_keyspace666"); err != topo.ErrNoNode {
		t.Errorf("GetSrvKeyspace(invalid): %v", err)
	}
	if s, err := ts.GetSrvKeyspace("cell", "test_keyspace"); err != nil || len(s.TabletTypes) != 1 || s.TabletTypes[0] != topo.TYPE_MASTER {
		t.Errorf("GetSrvKeyspace(valid): %v", err)
	}

}
