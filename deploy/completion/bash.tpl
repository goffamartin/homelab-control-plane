# Bash completion for HLCP. No eval, shell-generated commands or mutations.
_hlcp_complete()
{
    local candidate token i cword
    local -a words=()
    # Bash splits ':' in URLs by default. Rejoin literal tokens, never eval.
    for ((i=0; i<=COMP_CWORD; i++)); do
        token=${COMP_WORDS[i]}
        if [[ "$token" == : && ${#words[@]} -gt 0 ]]; then
            words[${#words[@]}-1]+=':'
        elif [[ $i -gt 0 && "${COMP_WORDS[i-1]}" == : && ${#words[@]} -gt 0 ]]; then
            words[${#words[@]}-1]+="$token"
        else
            words+=("$token")
        fi
    done
    cword=$((${#words[@]}-1))
    COMPREPLY=()
    while IFS= read -r candidate; do
        [[ -n "$candidate" ]] && COMPREPLY+=("$candidate")
    done < <(command "${words[0]}" __complete "${words[@]:1:cword}" 2>/dev/null)
    return 0
}
complete -F _hlcp_complete @COMMAND@
