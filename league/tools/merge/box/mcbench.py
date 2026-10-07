# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""mcbench.py <research_tools_v2 arguments> : the builders' harness, unchanged, with the user and system CPU seconds of every run added
to the pair rows of a bench (cand_user_s, cand_sys_s, inc_user_s, inc_sys_s). The harness file is imported, not modified."""
import importlib.util, sys
H = "/opt/league/restic-league-v2/research-tools/research_tools_v2.py"
spec = importlib.util.spec_from_file_location("rt2", H)
m = importlib.util.module_from_spec(spec); sys.modules["rt2"] = m; spec.loader.exec_module(m)
_slim, _pair_row = m.slim, m.pair_row
def slim(run):
    out = _slim(run)
    out["user_s"] = round(run["user_seconds"], 3); out["sys_s"] = round(run["system_seconds"], 3)
    return out
def pair_row(rep, order, c, i):
    row = _pair_row(rep, order, c, i)
    for k in ("user_s", "sys_s"):
        row["cand_" + k] = c.get(k); row["inc_" + k] = i.get(k)
    return row
m.slim, m.pair_row = slim, pair_row
sys.exit(m.main(sys.argv[1:]))
