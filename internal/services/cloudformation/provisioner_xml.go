package cloudformation

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
)

type cfnXMLListFunc func(name string, value any) ([]any, bool)
type cfnXMLItemNameFunc func(parent string) string
type cfnXMLListWrapperFunc func(parent string) string

func marshalCFNXML(root string, value any, topLevelList cfnXMLListFunc, itemName cfnXMLItemNameFunc, listWrapper cfnXMLListWrapperFunc) ([]byte, error) {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := encodeCFNXMLValue(enc, root, value, "", topLevelList, itemName, listWrapper); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeCFNXMLValue(enc *xml.Encoder, name string, value any, parent string, topLevelList cfnXMLListFunc, itemName cfnXMLItemNameFunc, listWrapper cfnXMLListWrapperFunc) error {
	if name == "" {
		return nil
	}
	if topLevelList != nil {
		if items, ok := topLevelList(name, value); ok {
			start := xml.StartElement{Name: xml.Name{Local: name}}
			if err := enc.EncodeToken(start); err != nil {
				return err
			}
			if err := encodeCFNXMLValue(enc, "Quantity", len(items), name, topLevelList, itemName, listWrapper); err != nil {
				return err
			}
			if err := encodeCFNXMLItems(enc, name, items, topLevelList, itemName, listWrapper); err != nil {
				return err
			}
			return enc.EncodeToken(start.End())
		}
	}

	start := xml.StartElement{Name: xml.Name{Local: name}}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	switch v := value.(type) {
	case map[string]any:
		for _, key := range sortedMapKeys(v) {
			if err := encodeCFNXMLValue(enc, key, v[key], name, topLevelList, itemName, listWrapper); err != nil {
				return err
			}
		}
	case []any:
		wrapperName := "Items"
		if listWrapper != nil {
			wrapperName = listWrapper(name)
		}
		if wrapperName == "" || name == "Items" {
			childName := itemName(parent)
			if name != "Items" {
				childName = itemName(name)
			}
			for _, item := range v {
				if err := encodeCFNXMLValue(enc, childName, item, name, topLevelList, itemName, listWrapper); err != nil {
					return err
				}
			}
		} else {
			if err := encodeCFNXMLItems(enc, name, v, topLevelList, itemName, listWrapper); err != nil {
				return err
			}
		}
	default:
		if err := enc.EncodeToken(xml.CharData([]byte(fmt.Sprint(v)))); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

func encodeCFNXMLItems(enc *xml.Encoder, parent string, items []any, topLevelList cfnXMLListFunc, itemName cfnXMLItemNameFunc, listWrapper cfnXMLListWrapperFunc) error {
	wrapperName := "Items"
	if listWrapper != nil {
		wrapperName = listWrapper(parent)
	}
	start := xml.StartElement{Name: xml.Name{Local: wrapperName}}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	childName := itemName(parent)
	for _, item := range items {
		if err := encodeCFNXMLValue(enc, childName, item, start.Name.Local, topLevelList, itemName, listWrapper); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

func cfnXMLItemsWrapper(parent string) string { return "Items" }

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
