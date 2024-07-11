import json
import multiprocessing
import sys
from dataclasses import dataclass, field
from typing import Any, Dict

from bagit import Bag, make_bag


@dataclass
class Command:
    name: str
    args: Dict[str, Any] = field(default_factory=dict)


class Runner:
    def __init__(self, cmd, stdout):
        self.cmd = cmd
        self.stdout = stdout

    def run(self):
        name = self.cmd.name
        args = self.cmd.args

        resp = {}
        try:
            if name == "validate":
                resp = self.validate(args)
            elif name == "make":
                resp = self.make(args)
            else:
                raise Exception("Unknown command")
        except BaseException as err:
            self.write_error(self.stdout, err)
            return

        self.write(self.stdout, resp)

    def validate(self, args):
        bag = Bag(args.get("path"))
        bag.validate(processes=multiprocessing.cpu_count())
        return {"valid": True}

    def make(self, args):
        bag_dir = args.pop("path")
        bag = make_bag(bag_dir, **args)
        return {"version": bag.version}

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

        if cmd.name == "exit":
            break

        runner = Runner(cmd, sys.stdout)
        runner.run()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
