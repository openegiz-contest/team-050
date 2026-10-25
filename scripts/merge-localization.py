"""Extend a fresh XLIFF export with the previous handoff's IDs and translations.

Usage: python scripts/merge-localization.py BASE.xlf CURRENT.xlf
The second file is updated. XML mixed-content whitespace is preserved.
"""
from pathlib import Path
import copy
import sys
import xml.etree.ElementTree as ET

NS = "urn:oasis:names:tc:xliff:document:2.0"
Q = {"x": NS}
ET.register_namespace("", NS)

def source_text(unit):
    element = unit.find("x:segment/x:source", Q)
    return (element.text or "") + "".join(child.get("equiv", "") + (child.tail or "") for child in element)

def main():
    if len(sys.argv) != 3:
        raise SystemExit("Usage: merge-localization.py BASE.xlf CURRENT.xlf")
    baseline = ET.parse(sys.argv[1]).getroot()
    tree = ET.parse(sys.argv[2])
    root = tree.getroot()
    output = root.find("x:file", Q)
    if output is None or root.get("srcLang") != baseline.get("srcLang") or root.get("trgLang") != baseline.get("trgLang"):
        raise SystemExit("Incompatible XLIFF language pair or file structure")
    current = {unit.get("id"): unit for unit in root.findall(".//x:unit", Q)}
    retained = translated = 0
    for unit in baseline.findall(".//x:unit", Q):
        identity = unit.get("id")
        if identity not in current:
            restored = copy.deepcopy(unit)
            notes = restored.find("x:notes", Q)
            if notes is None:
                notes = ET.SubElement(restored, f"{{{NS}}}notes")
            ET.SubElement(notes, f"{{{NS}}}note", {"category": "version"}).text = "Сохранено из первоначального пакета для совместимости; в текущем интерфейсе строка могла измениться."
            output.append(restored)
            current[identity] = restored
            retained += 1
        elif source_text(unit) != source_text(current[identity]):
            raise SystemExit(f"Source changed under the same ID: {identity}")
        else:
            old_segment = unit.find("x:segment", Q)
            old_target = old_segment.find("x:target", Q)
            segment = current[identity].find("x:segment", Q)
            target = segment.find("x:target", Q)
            if old_target is not None and "".join(old_target.itertext()).strip() and (target is None or not "".join(target.itertext()).strip()):
                if target is not None:
                    segment.remove(target)
                segment.append(copy.deepcopy(old_target))
                segment.set("state", old_segment.get("state", "translated"))
                translated += 1
    # ET.indent changes significant spaces around inline placeholder tags.
    destination = Path(sys.argv[2])
    temporary = destination.with_suffix(destination.suffix + ".tmp")
    tree.write(temporary, encoding="utf-8", xml_declaration=True)
    temporary.replace(destination)
    print(f"Retained legacy units: {retained}; carried translations: {translated}; total: {len(current)}")

if __name__ == "__main__":
    main()
