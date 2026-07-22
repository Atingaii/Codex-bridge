From Coq Require Import List.
Import ListNotations.

Fixpoint reverse_accumulator {A : Type} (values accumulator : list A) : list A :=
  match values with
  | [] => accumulator
  | value :: remaining => reverse_accumulator remaining (value :: accumulator)
  end.

Theorem reverse_accumulator_correct :
  forall (A : Type) (values accumulator : list A),
    reverse_accumulator values accumulator = rev values ++ accumulator.
Proof.
  (* Prove the accumulator invariant without changing the function. *)
Admitted.
