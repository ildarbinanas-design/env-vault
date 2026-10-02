"""Require execution of platform-sensitive tests, not just a green go test exit."""
import json
import sys

required = ({"TestSecretSetStdinRefusesATerminal",
             "TestChildKilledBySignalReportsTheSignal",
             "TestForwardSignalsSkipsInterruptsTheTerminalAlreadyDelivered",
             "TestForwardSignalsPassesInterruptsWithoutATerminal",
             "TestForwardSignalsPassesInterruptsToAChildInAnotherGroup",
             "TestIgnoredAtStartSeesAnInheritedIgnoredSignal",
             "TestChildKeepsASignalIgnoredAtStart",
             "TestExitBySignalEndsTheProcessWithThatSignal"}
            if sys.platform == "darwin" else
            {"TestWindowsNativeCredentialIdentity", "TestWindowsEnumerationMetadataAndErrors",
             "TestWindowsImportCollisionsBeforeAnyWrite", "TestChildReceivesEnv",
             "TestChildExitCodePropagated"})
passed, failed = set(), False
for line in sys.stdin:
    event = json.loads(line)
    action, name = event.get("Action"), event.get("Test", "")
    if action == "pass":
        passed.add(name)
    if action == "fail":
        failed = True
    # Go diagnostics in these suites have their own leak assertions. Show the
    # test stream so failures remain actionable, and prove required names ran.
    if action == "output":
        sys.stdout.write(event.get("Output", ""))
if failed or required - passed:
    print("FAIL: required native tests did not all pass:", sorted(required - passed))
    raise SystemExit(1)
print("Required native scenarios executed and passed:", ", ".join(sorted(required)))
