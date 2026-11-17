# outputs file\tline\tlevel\town_words\tsubtree_words\theading
FNR==1 { if (n>0) flush(); n=0; infence=0 }
/^```/ { infence=!infence }
{
  if (!infence && $0 ~ /^#+ /) {
    match($0,/^#+/); lvl=RLENGTH
    n++; F[n]=FILENAME; L[n]=FNR; LV[n]=lvl; H[n]=$0; W[n]=0
    next
  }
  if (n>0) W[n]+=NF
}
function flush(   i,j,s) {
  for(i=1;i<=n;i++){ s=W[i]; for(j=i+1;j<=n && LV[j]>LV[i];j++) s+=W[j]; printf "%s\t%d\t%d\t%d\t%d\t%s\n", F[i],L[i],LV[i],W[i],s,H[i] }
}
END { flush() }
