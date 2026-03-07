// internal/aoi.go
package internal

import (
	"sync"
)

type AOIManager struct {
	MinX, MaxX, MinZ, MaxZ int
	GridsX, GridsZ         int
	grids                  map[int]*Grid
}

type Grid struct {
	players map[string]*User
	lock    sync.RWMutex
}

func NewAOIManager(minX, maxX, minZ, maxZ, gridSize int) *AOIManager {
	am := &AOIManager{
		MinX: minX, MaxX: maxX, MinZ: minZ, MaxZ: maxZ,
		GridsX: (maxX - minX) / gridSize,
		GridsZ: (maxZ - minZ) / gridSize,
		grids:  make(map[int]*Grid),
	}
	for z := 0; z < am.GridsZ; z++ {
		for x := 0; x < am.GridsX; x++ {
			gid := z*am.GridsX + x
			am.grids[gid] = &Grid{players: make(map[string]*User)}
		}
	}
	return am
}

func (am *AOIManager) GetGridIDByPos(x, z float32) int {
	gx := (int(x) - am.MinX) / Conf.AOI.GridSize
	gz := (int(z) - am.MinZ) / Conf.AOI.GridSize
	return gz*am.GridsX + gx
}

func (am *AOIManager) GetSurroundingGridIDs(gid int) (gridIDs []int) {
	gridIDs = append(gridIDs, gid)
	x, z := gid%am.GridsX, gid/am.GridsX
	for i := -1; i <= 1; i++ {
		for j := -1; j <= 1; j++ {
			if i == 0 && j == 0 {
				continue
			}
			newX, newZ := x+j, z+i
			if newX >= 0 && newX < am.GridsX && newZ >= 0 && newZ < am.GridsZ {
				gridIDs = append(gridIDs, newZ*am.GridsX+newX)
			}
		}
	}
	return
}

func (am *AOIManager) GetPlayersInGrids(gridIDs []int) (players []*User) {
	for _, gid := range gridIDs {
		am.grids[gid].lock.RLock()
		for _, player := range am.grids[gid].players {
			players = append(players, player)
		}
		am.grids[gid].lock.RUnlock()
	}
	return
}

func (am *AOIManager) AddPlayerToGrid(user *User, gid int) {
	am.grids[gid].lock.Lock()
	am.grids[gid].players[user.Name] = user
	am.grids[gid].lock.Unlock()
}

func (am *AOIManager) RemovePlayerFromGrid(user *User, gid int) {
	am.grids[gid].lock.Lock()
	delete(am.grids[gid].players, user.Name)
	am.grids[gid].lock.Unlock()
}
