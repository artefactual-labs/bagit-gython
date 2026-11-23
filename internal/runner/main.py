import json
import multiprocessing
import os
import stat
import sys
from collections import Counter
from dataclasses import dataclass, field
from heapq import nsmallest
from itertools import zip_longest
from typing import Any, Dict

from bagit import (
    Bag,
    BagError,
    BagValidationError,
    FileNormalizationConflict,
    make_bag,
    normalize_unicode,
)


MAX_COMPLETENESS_REPLY_BYTES = 16 * 1024
MAX_COMPLETENESS_EXAMPLES = 10
MAX_COMPLETENESS_MESSAGE_CHARS = 512


@dataclass
class Command:
    name: str
    args: Dict[str, Any] = field(default_factory=dict)


class UnknownCommandError(Exception):
    pass


class ExitError(Exception):
    pass


class CompletenessValidationError(BagValidationError):
    def __init__(self, message, differences):
        super().__init__(message)
        self.differences = differences


def path_mode(path):
    try:
        return os.stat(path).st_mode
    except (FileNotFoundError, NotADirectoryError):
        return 0


def remember_path(names, path):
    normalized = normalize_unicode(path)
    previous = names.setdefault(normalized, path)
    if previous != path:
        raise FileNormalizationConflict(previous, path)
    return normalized


class ManifestReader(Bag):
    """Use the upstream parser on one manifest to keep its entries separate."""

    def __init__(self, bag, filename):
        self.path = bag.path
        self.encoding = bag.encoding
        self.version_info = bag.version_info
        self.filename = filename
        self.algorithms = []
        self.normalized_manifest_names = {}
        self._path_is_dangerous = bag._path_is_dangerous
        self._load_manifests()

    def manifest_files(self):
        return (self.filename,)

    def tagmanifest_files(self):
        return ()


class CompletenessBag(Bag):
    """Check one staged bag with Bag's parsing and comparison rules."""

    def _open(self):
        self._payload_scanned = False
        # Enumerating the root also detects an unavailable staging directory.
        self._root_names = os.listdir(self.path)
        path_mode(os.path.join(self.path, "bagit.txt"))
        try:
            super()._open()
        except (AttributeError, TypeError):
            for tag in ("BagIt-Version", "Tag-File-Character-Encoding"):
                if isinstance(self.tags.get(tag), list):
                    raise BagError(f"Duplicate {tag} in bagit.txt") from None
            raise
        except LookupError as err:
            # A registered binary codec can pass codecs.lookup but fail open().
            if type(err) is LookupError:
                raise BagError(str(err)) from err
            raise
        path_mode(os.path.join(self.path, self.tag_file_name))

    def _path_is_dangerous(self, path):
        if "\x00" in path:
            return True
        try:
            return super()._path_is_dangerous(path)
        except ValueError:
            # Invalid platform paths can fail realpath or commonpath.
            return True

    def manifest_files(self):
        return self._manifest_files("manifest")

    def tagmanifest_files(self):
        return self._manifest_files("tagmanifest")

    def _manifest_files(self, prefix):
        for name in self._root_names:
            if name.startswith(prefix + "-") and name.endswith(".txt"):
                path = os.path.join(self.path, name)
                if stat.S_ISREG(path_mode(path)):
                    yield path

    def _load_manifests(self):
        payload_prefix = "data" + os.sep
        self._payload_names = {}
        self._tag_names = {}
        listings = Counter()
        manifests = list(self.manifest_files())
        for filename in manifests:
            for path in ManifestReader(self, filename).entries:
                if not path.startswith(payload_prefix):
                    raise BagError(f"Payload manifest lists a tag path {path!r}")
                name = remember_path(self._payload_names, path)
                listings[name] += 1

        self._partially_listed_payload = set()
        if self.version_info >= (1, 0):
            self._partially_listed_payload = {
                name for name, count in listings.items() if count < len(manifests)
            }

        if self.version_info >= (0, 97):
            for filename in self.tagmanifest_files():
                for path in ManifestReader(self, filename).entries:
                    if path.startswith(payload_prefix):
                        raise BagError(f"Tag manifest lists a payload path {path!r}")
                    remember_path(self._tag_names, path)

    def _validate_structure_payload_directory(self):
        path_mode(os.path.join(self.path, "data"))
        super()._validate_structure_payload_directory()

    def validate_fetch(self):
        path_mode(os.path.join(self.path, "fetch.txt"))
        try:
            super().validate_fetch()
        except ValueError as err:
            raise BagError(f"Malformed fetch.txt: {err}") from err

    def _validate_oxum(self):
        try:
            super()._validate_oxum()
        except ValueError as err:
            raise BagError("Malformed Payload-Oxum value") from err
        except BagValidationError as err:
            # Oxum found a mismatch before the membership comparison could run.
            differences = None
            try:
                differences = self.compare_manifests_with_fs()
            except Exception:
                # A diagnostics failure leaves the Oxum verdict intact.
                pass
            raise CompletenessValidationError(err.message, differences) from err

    def _validate_completeness(self):
        missing, unexpected = self.compare_manifests_with_fs()
        if missing or unexpected:
            raise CompletenessValidationError(
                "Bag is incomplete", (missing, unexpected)
            )

    def compare_manifests_with_fs(self):
        if not self._payload_scanned:
            for _ in self.payload_files():
                pass

        files = self.normalized_filesystem_names
        missing = [
            path for name, path in self._payload_names.items() if name not in files
        ]
        missing.extend(
            path
            for path in self._tag_names.values()
            if not stat.S_ISREG(path_mode(os.path.join(self.path, path)))
        )
        unexpected = [
            path
            for name, path in files.items()
            if name not in self._payload_names or name in self._partially_listed_payload
        ]
        return missing, unexpected

    def payload_files(self):
        # os.walk can hide scan and entry errors. Keep its traversal semantics,
        # including following a symlink at data/ but skipping nested directory links.
        pending = [os.path.join(self.path, "data")]
        while pending:
            subdirs = []
            with os.scandir(pending.pop()) as entries:
                for entry in entries:
                    if entry.is_symlink():
                        mode = path_mode(entry.path)
                        if not mode:
                            path = os.path.relpath(entry.path, self.path)
                            raise BagError(f"Dangling payload symlink {path!r}")
                        if stat.S_ISDIR(mode):
                            continue
                    elif entry.is_dir():
                        subdirs.append(entry.path)
                        continue
                    path = os.path.relpath(entry.path, self.path)
                    remember_path(self.normalized_filesystem_names, path)
                    yield path
            pending.extend(reversed(subdirs))
        self._payload_scanned = True


def completeness_failure(status, message):
    if len(message) > MAX_COMPLETENESS_MESSAGE_CHARS:
        message = message[: MAX_COMPLETENESS_MESSAGE_CHARS - 1] + "…"
    return {"status": status, "message": message}


def add_completeness_differences(reply, missing, unexpected):
    examples = []
    reply["differences"] = {
        "missing": len(missing),
        "unexpected": len(unexpected),
        "examples": examples,
    }

    limit = MAX_COMPLETENESS_REPLY_BYTES - len(os.linesep)
    example_budget = limit - len(json.dumps(reply))

    def eligible_paths(paths, kind):
        path_budget = example_budget - len(json.dumps({"kind": kind, "path": ""}))
        for path in paths:
            if os.sep != "/":
                path = path.replace(os.sep, "/")
            # A POSIX filename can contain bytes with no UTF-8 representation.
            try:
                path.encode("utf-8")
            except UnicodeEncodeError:
                continue
            # A character can take up to two six-byte JSON escapes.
            if (
                12 * len(path) <= path_budget
                or len(json.dumps(path)) - 2 <= path_budget
            ):
                yield path

    # Unicode code point order gives the same ordering as valid UTF-8 bytes.
    missing_sample = nsmallest(
        MAX_COMPLETENESS_EXAMPLES, eligible_paths(missing, "missing")
    )
    unexpected_sample = nsmallest(
        MAX_COMPLETENESS_EXAMPLES, eligible_paths(unexpected, "unexpected")
    )
    for pair in zip_longest(missing_sample, unexpected_sample):
        for kind, path in zip(("missing", "unexpected"), pair):
            if path is None:
                continue
            examples.append({"kind": kind, "path": path})
            if len(json.dumps(reply)) > limit:
                examples.pop()
            if len(examples) == MAX_COMPLETENESS_EXAMPLES:
                return


class Runner:
    ALLOWED_COMMANDS = ("validate", "check_completeness", "make", "exit")
    ALLOWED_COMMANDS_LIST = ", ".join(ALLOWED_COMMANDS)

    def __init__(self, cmd, stdout):
        self.cmd = cmd
        self.stdout = stdout

    def run(self):
        name = self.cmd.name
        args = self.cmd.args

        resp = {}
        try:
            ret = self.get_handler(name)(args)
            resp.update(ret)
        except ExitError:
            raise
        except BaseException as err:
            self.write_error(self.stdout, err)
            return

        self.write(self.stdout, resp)

    def get_handler(self, name):
        if name not in self.ALLOWED_COMMANDS:
            raise UnknownCommandError(
                f"'{name}' is not a valid command, use: {self.ALLOWED_COMMANDS_LIST}"
            )
        handler = getattr(self, f"{name}_handler")
        if handler is None:
            raise UnknownCommandError(f"'{name}' does not have a handler")
        return handler

    def validate_handler(self, args):
        bag = Bag(args.get("path"))
        bag.validate(processes=multiprocessing.cpu_count())
        return {"valid": True}

    def check_completeness_handler(self, args):
        try:
            path = args["path"]
            if not isinstance(path, str) or not path:
                return completeness_failure("error", "A bag directory path is required")
            bag = CompletenessBag(path)
            bag.validate(completeness_only=True)
        except CompletenessValidationError as err:
            reply = completeness_failure("invalid", err.message)
            if err.differences is not None:
                add_completeness_differences(reply, *err.differences)
            return reply
        except BagValidationError as err:
            return completeness_failure("invalid", err.message)
        except (BagError, UnicodeDecodeError) as err:
            return completeness_failure("invalid", str(err))
        except Exception as err:
            return completeness_failure("error", f"{type(err).__name__}: {err}")

        return {"status": "complete"}

    def make_handler(self, args):
        bag_dir = args.pop("path")
        bag = make_bag(bag_dir, **args)
        return {"version": bag.version}

    def exit_handler(self, args):
        raise ExitError

    @staticmethod
    def write(stdout, resp):
        print(json.dumps(resp), file=stdout, flush=True)

    @staticmethod
    def write_error(stdout, err):
        Runner.write(stdout, {"err": str(err), "type": err.__class__.__name__})


def main():
    while True:
        line = sys.stdin.readline()
        if not line:
            break

        try:
            payload = json.loads(line)
        except ValueError as err:
            Runner.write_error(sys.stdout, err)
            continue

        cmd = Command(name=payload.get("name"), args=payload.get("args"))

        runner = Runner(cmd, sys.stdout)
        try:
            runner.run()
        except ExitError:
            return


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
