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


class UnknownCommandError(Exception):
    pass


class ExitError(Exception):
    pass


class Runner:
    ALLOWED_COMMANDS = ("validate", "make", "exit")
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
