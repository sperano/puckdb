package store

import (
	"encoding/xml"
)

// ParseXML parses Yahoo Fantasy XML content.
func ParseXML(data []byte) (*FantasyContent, error) {
	var fantasy FantasyContent
	if err := xml.Unmarshal(data, &fantasy); err != nil {
		return nil, err
	}
	return &fantasy, nil
}
