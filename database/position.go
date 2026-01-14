package database

import (
	"fmt"
	"strings"
)

type PositionName int

const (
	BN     PositionName = 1
	IR     PositionName = 2
	IRPlus PositionName = 4
	C      PositionName = 8
	LW     PositionName = 16
	RW     PositionName = 32
	D      PositionName = 64
	Util   PositionName = 128
	G      PositionName = 256

	// TODO more from other league!!!
)

const (
	sBN     = "BN"
	sIR     = "IR"
	sIRPlus = "IR+"
	sC      = "C"
	sLW     = "LW"
	sRW     = "RW"
	sD      = "D"
	sUtil   = "Util"
	sG      = "G"
)

func (rp PositionName) String() string {
	tokens := make([]string, 0)
	if rp&C > 0 {
		tokens = append(tokens, sC)
	}
	if rp&LW > 0 {
		tokens = append(tokens, sLW)
	}
	if rp&RW > 0 {
		tokens = append(tokens, sRW)
	}
	if rp&D > 0 {
		tokens = append(tokens, sD)
	}
	if rp&Util > 0 {
		tokens = append(tokens, sUtil)
	}
	if rp&G > 0 {
		tokens = append(tokens, sG)
	}
	if rp&BN > 0 {
		tokens = append(tokens, sBN)
	}
	if rp&IR > 0 {
		tokens = append(tokens, sIR)
	}
	if rp&IRPlus > 0 {
		tokens = append(tokens, sIRPlus)
	}
	return strings.Join(tokens, ",")
}

func ScanPositionName(str string) (PositionName, error) {
	tokens := strings.Split(str, ",")
	var p PositionName
	for _, tok := range tokens {
		switch tok {
		case sBN:
			p |= BN
		case sIR:
			p |= IR
		case sIRPlus:
			p |= IRPlus
		case sC:
			p |= C
		case sLW:
			p |= LW
		case sRW:
			p |= RW
		case sD:
			p |= D
		case sUtil:
			p |= Util
		case sG:
			p |= G
		default:
			return -1, fmt.Errorf("can't parse RosterPosition: %s", str)
		}
	}
	return p, nil
}

type PositionType int

const (
	Skater PositionType = iota
	Goaltender
	BothPositions
)

var positionTypes = map[string]PositionType{
	"P": Skater,
	"G": Goaltender,
	"":  BothPositions,
}

func ScanPositionType(data string) (PositionType, error) {
	pt, ok := positionTypes[data]
	if !ok {
		return 0, fmt.Errorf("invalid position type: %s", data)
	}
	return pt, nil
}

// Stats

//type StatName int
//
//const (
//	Goals StatName = iota
//	Assists
//	PlusMinus
//	Penalties
//	PowerPlayPoints
//	ShotsOnGoal
//	Wins
//	GoalsAgainst
//	GoalsAgainstAverage
//)
//
//var statNames = map[string]StatName{
//	"G":   Goals,
//	"A":   Assists,
//	"+/-": PlusMinus,
//	"PIM": Penalties,
//	"PPP": PowerPlayPoints,
//	"SOG": ShotsOnGoal,
//	"W":   Wins,
//	"GA":  GoalsAgainst,
//	"GAA": GoalsAgainstAverage,
//}
