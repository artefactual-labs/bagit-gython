import json
import multiprocessing
import sys

from bagit import Bag, make_bag
from bagit import BagError


class Runner:
    def __init__(self, req, stdout):
        self.req = req
        self.stdout = stdout

    @property
    def name(self):
        return self.req.get("name")

    @property
    def args(self):
        return self.req.get("args")

    def run(self):
        resp = {}

        try:
            if self.name == "validate":
                resp = self.validate(self.args)
            elif self.name == "make":
                resp = self.make(self.args)
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
        cmd = sys.stdin.readline()
        if not cmd:
            break

        req = json.loads(cmd)
        if req.get("name") == "exit":
            break

        runner = Runner(req, sys.stdout)
        runner.run()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
