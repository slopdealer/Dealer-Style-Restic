# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
import json, statistics as st, sys
O='/opt/league/restic-final/out/'
TAG=(sys.argv[1]+'-') if len(sys.argv)>1 and not sys.argv[1].startswith('--') else ''
WL=['main','big-index','large-files','remote']
PH=['backup-initial','backup-unchanged','backup-mutated','restore','check-read-data','forget-prune','ls-json']
SH={'backup-initial':'bi','backup-unchanged':'bu','backup-mutated':'bm','restore':'rs','check-read-data':'ck','forget-prune':'fp','ls-json':'ls'}
res={}
for n in (8,16):
    for w in WL:
        d=json.load(open(O+'mc%d-%s%s.json'%(n,TAG,w)))
        res[(n,w,'pass')]=d['worth']['point_estimate']
        for p in PH:
            pr=d['phases'][p]['pairs']
            sv=[x['time_saved_pct'] for x in pr]
            m=lambda k:st.mean(x[k] for x in pr)
            res[(n,w,p)]=(st.median(sv),sum(1 for x in pr if x['cand_s']<x['inc_s']),len(pr),m('cand_s'),m('inc_s'),m('cand_user_s'),m('inc_user_s'),m('cand_sys_s'),m('inc_sys_s'),min(sv),max(sv))
print('cell        8c saved%(w) [min..max]   16c saved%(w) [min..max] | 8c wall c/s  user c/s  sys c/s | 16c wall c/s  user c/s  sys c/s')
for w in WL:
    for p in PH:
        a=res[(8,w,p)]; b=res[(16,w,p)]
        f=lambda r:'%+6.2f(%d/%d) [%+.1f..%+.1f]'%(r[0],r[1],r[2],r[9],r[10])
        g=lambda r:'%.2f/%.2f %.1f/%.1f %.1f/%.1f'%(r[3],r[4],r[5],r[6],r[7],r[8])
        print('%-11s %s %s | %s | %s'%(w[:8]+' '+SH[p],f(a),f(b),g(a),g(b)))
    print('%-11s pass estimate 8c %+.2f   16c %+.2f'%(w[:8],res[(8,w,'pass')],res[(16,w,'pass')]))

if '--md' in sys.argv:
    L={'bi':'backup-initial','bu':'backup-unchanged','bm':'backup-mutated','rs':'restore','ck':'check-read-data','fp':'forget-prune','ls':'ls-json'}
    for w in WL:
        for p in PH:
            a=res[(8,w,p)]; b=res[(16,w,p)]
            print('MD|%s|%s|%+.2f|%d/%d|%+.2f|%d/%d|%.1f / %.1f|%.1f / %.1f|%.1f / %.1f|%.1f / %.1f'%(w,p,a[0],a[1],a[2],b[0],b[1],b[2],a[5],a[6],b[5],b[6],a[7],a[8],b[7],b[8]))
        print('MD|%s|pass|%+.2f||%+.2f||||'%(w,res[(8,w,'pass')],res[(16,w,'pass')]))
