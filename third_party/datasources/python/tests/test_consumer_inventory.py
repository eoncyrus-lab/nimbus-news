from __future__ import annotations

import pathlib
import re
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parents[3]
CONSUMER_ROOTS = (
    REPO_ROOT / "nimbus" / "skills",
    REPO_ROOT / "nimbus" / ".agents" / "skills",
    REPO_ROOT / "news" / "scripts",
    REPO_ROOT / "equity-screener" / "src",
    REPO_ROOT / "services",
)
DIRECT_IMPORT = re.compile(
    r"^[ \t]*(?:import[ \t]+data_access(?:[ \t]+as[ \t]+\w+)?|"
    r"from[ \t]+data_access[ \t]+import[ \t]+(?!legacy\b))",
    re.MULTILINE,
)
LEGACY_IMPORT = re.compile(
    r"^[ \t]*from[ \t]+data_access[ \t]+import[ \t]+legacy[ \t]+as[ \t]+\w+",
    re.MULTILINE,
)


def consumer_files() -> list[pathlib.Path]:
    files: list[pathlib.Path] = []
    for root in CONSUMER_ROOTS:
        for path in root.rglob("*.py"):
            if "portfolio-manager" not in path.parts:
                files.append(path)
    return files


class ConsumerInventoryTests(unittest.TestCase):
    def test_all_unupgraded_consumers_import_legacy_explicitly(self) -> None:
        direct: list[str] = []
        legacy_count = 0
        legacy_files: set[pathlib.Path] = set()
        for path in consumer_files():
            text = path.read_text(encoding="utf-8")
            if DIRECT_IMPORT.search(text):
                direct.append(str(path.relative_to(REPO_ROOT)))
            matches = LEGACY_IMPORT.findall(text)
            if matches:
                legacy_count += len(matches)
                legacy_files.add(path)
        self.assertEqual(direct, [], "top-level strict imports need an explicit migration")
        self.assertEqual(legacy_count, 114)
        self.assertEqual(len(legacy_files), 72)

    def test_nimbus_skill_trees_have_matching_inventory(self) -> None:
        canonical = REPO_ROOT / "nimbus" / "skills"
        agents = REPO_ROOT / "nimbus" / ".agents" / "skills"

        def relative_inventory(root: pathlib.Path) -> set[pathlib.Path]:
            return {
                path.relative_to(root)
                for path in root.rglob("*.py")
                if "portfolio-manager" not in path.parts
                and LEGACY_IMPORT.search(path.read_text(encoding="utf-8"))
            }

        self.assertEqual(relative_inventory(canonical), relative_inventory(agents))


if __name__ == "__main__":
    unittest.main()

